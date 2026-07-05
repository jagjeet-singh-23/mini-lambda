package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/builder"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/queue"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/buildlog"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

type workerDeps struct {
	rc              *redis.Client
	s3Storage       *storage.S3Storage
	zipProcessor    *builder.ZIPProcessor
	dockerBuilder   *builder.DockerBuilder
	webhookNotifier *builder.WebhookNotifier
	publisher       *queue.Publisher
}

const buildLogTTL = 4 * time.Hour

func streamBuildLog(ctx context.Context, rc *redis.Client, jobID, message string) {
	if err := buildlog.Append(ctx, rc, jobID, message); err != nil {
		logger.Warn("streamBuildLog: failed to write to Redis stream", "job_id", jobID, "error", err)
	}
}

func expireBuildLog(ctx context.Context, rc *redis.Client, jobID string) {
	if err := buildlog.Expire(ctx, rc, jobID, buildLogTTL); err != nil {
		logger.Warn("expireBuildLog: failed to set TTL", "job_id", jobID, "error", err)
	}
}

// failBuild logs the terminal failure sentinel, expires the log stream, and
// returns the wrapped error via handleBuildFailureWithMetrics — collapsing
// the repeated 3-line pattern every failure branch in processBuildJob needs.
func failBuild(ctx context.Context, deps workerDeps, job *builder.BuildJob, startTime time.Time, format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	streamBuildLog(ctx, deps.rc, job.ID, "__BUILD_FAILED__: "+msg)
	expireBuildLog(ctx, deps.rc, job.ID)
	return handleBuildFailureWithMetrics(ctx, job, deps.webhookNotifier, msg, startTime)
}

func main() {
	log.Println("🚀 Build Service Worker starting...")

	// Start metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		metricsPort := getEnv("METRICS_PORT", "9092")
		log.Printf("📊 Metrics server listening on :%s", metricsPort)
		if err := http.ListenAndServe(":"+metricsPort, mux); err != nil {
			log.Printf("❌ Metrics server error: %v", err)
		}
	}()

	// Configuration
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	s3Endpoint := getEnv("S3_ENDPOINT", "")
	s3Region := getEnv("S3_REGION", "us-east-1")
	s3Bucket := getEnv("S3_BUCKET", "mini-lambda")
	s3AccessKey := getEnv("S3_ACCESS_KEY", "")
	s3SecretKey := getEnv("S3_SECRET_KEY", "")
	workDir := getEnv("WORK_DIR", "/tmp/build-worker")

	// Initialize RabbitMQ consumer
	consumer, err := queue.NewConsumer(amqpURL)
	if err != nil {
		log.Fatalf("❌ Failed to initialize RabbitMQ consumer: %v", err)
	}
	defer consumer.Close()

	log.Println("✅ RabbitMQ consumer initialized")

	// Initialize RabbitMQ publisher (for function.built events)
	publisher, err := queue.NewPublisher(amqpURL)
	if err != nil {
		log.Fatalf("❌ Failed to initialize RabbitMQ publisher: %v", err)
	}
	defer publisher.Close()

	log.Println("✅ RabbitMQ publisher initialized")

	// Initialize S3 storage
	s3Storage, err := storage.NewS3Storage(s3Endpoint, s3Region, s3Bucket, s3AccessKey, s3SecretKey)
	if err != nil {
		log.Fatalf("❌ Failed to initialize S3 storage: %v", err)
	}

	log.Println("✅ S3 storage initialized")

	// Initialize Redis for Streaming
	redisAddr := getEnv("REDIS_CACHE_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	log.Println("✅ Redis streaming client initialized")

	// Initialize ZIP processor
	zipProcessor := builder.NewZIPProcessor(workDir)

	// Initialize Docker builder
	ecrRepo := getEnv("ECR_REGISTRY_URL", "") // e.g. 123456.dkr.ecr.us-east-1.amazonaws.com/mini-lambda
	dockerBuilder := builder.NewDockerBuilder(workDir, ecrRepo, redisClient)

	// Initialize webhook notifier
	webhookNotifier := builder.NewWebhookNotifier()

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("🛑 Shutting down build worker...")
		cancel()
	}()

	log.Println("✅ Build Service Worker ready to process jobs")

	deps := workerDeps{
		rc:              redisClient,
		s3Storage:       s3Storage,
		zipProcessor:    zipProcessor,
		dockerBuilder:   dockerBuilder,
		webhookNotifier: webhookNotifier,
		publisher:       publisher,
	}

	// Start consuming build jobs
	err = consumer.Consume("build-jobs", func(body []byte) error {
		return processBuildJob(ctx, deps, body)
	})

	if err != nil {
		log.Fatalf("❌ Consumer error: %v", err)
	}

	log.Println("✅ Build worker stopped gracefully")
}

func processBuildJob(ctx context.Context, deps workerDeps, body []byte) error {
	// Parse build job
	var job builder.BuildJob
	if err := json.Unmarshal(body, &job); err != nil {
		logger.Error("Failed to parse build job", "error", err)
		return err
	}

	logger.Info("Processing build job",
		"job_id", job.ID,
		"function_id", job.FunctionID,
		"runtime", job.Runtime,
	)

	// Update status to building
	startTime := time.Now()
	job.StartedAt = &startTime
	job.Status = string(builder.StatusBuilding)

	// Notify: building started
	if err := deps.webhookNotifier.NotifyBuilding(ctx, job.WebhookURL, job.ID); err != nil {
		logger.Error("Failed to send building webhook", "error", err)
	}

	// Branch Logic: Native Docker Build vs. Legacy Zip Extraction
	if job.RepoURL != "" {
		logger.Info("Starting GitHub Docker build", "function_id", job.FunctionID, "repo", job.RepoURL)

		imageURI, err := deps.dockerBuilder.CloneAndBuild(ctx, job.ID, job.FunctionID, job.RepoURL, job.Dockerfile)
		if err != nil {
			return failBuild(ctx, deps, &job, startTime, "Docker build failed: %v", err)
		}

		// Save the ECR Image URI to S3 so lambda-service knows what to pull
		_, err = deps.s3Storage.UploadPackage(ctx, job.FunctionID+"_imageuri", []byte(imageURI))
		if err != nil {
			return failBuild(ctx, deps, &job, startTime, "Failed to save image URI metadata: %v", err)
		}

		logger.Info("GitHub Docker build completed", "image_uri", imageURI)

	} else {
		logger.Info("Starting legacy S3 Zip extraction", "function_id", job.FunctionID)

		streamBuildLog(ctx, deps.rc, job.ID, "Downloading package from S3...")
		packageData, err := deps.s3Storage.DownloadPackage(ctx, job.PackageURL)
		if err != nil {
			return failBuild(ctx, deps, &job, startTime, "Failed to download package: %v", err)
		}

		streamBuildLog(ctx, deps.rc, job.ID, "Validating ZIP package...")
		if err := deps.zipProcessor.ValidatePackage(packageData); err != nil {
			return failBuild(ctx, deps, &job, startTime, "Invalid package: %v", err)
		}

		streamBuildLog(ctx, deps.rc, job.ID, "Extracting ZIP package...")
		extractedDir, err := deps.zipProcessor.Process(ctx, packageData, job.FunctionID)
		if err != nil {
			return failBuild(ctx, deps, &job, startTime, "Failed to process package: %v", err)
		}
		defer deps.zipProcessor.Cleanup(job.FunctionID)

		streamBuildLog(ctx, deps.rc, job.ID, "Uploading artifacts to S3...")
		if err := deps.s3Storage.UploadDirectory(ctx, job.FunctionID, extractedDir); err != nil {
			return failBuild(ctx, deps, &job, startTime, "Failed to upload artifacts: %v", err)
		}

		streamBuildLog(ctx, deps.rc, job.ID, "Publishing function.built event...")
		evt := domain.FunctionBuiltEvent{
			FunctionID:  job.FunctionID,
			Name:        job.Name,
			Runtime:     job.Runtime,
			Handler:     job.Handler,
			S3Prefix:    fmt.Sprintf("functions/%s/", job.FunctionID),
			MemoryMB:    job.MemoryMB,
			TimeoutSecs: job.TimeoutSecs,
			BuiltAt:     time.Now(),
		}
		if err := deps.publisher.Publish(ctx, domain.FunctionBuiltQueue, evt); err != nil {
			logger.Error("Failed to publish function.built event", "function_id", job.FunctionID, "error", err)
		}
	}

	expireBuildLog(ctx, deps.rc, job.ID)
	streamBuildLog(ctx, deps.rc, job.ID, "__BUILD_DONE__")

	// Mark as completed
	completedTime := time.Now()
	job.CompletedAt = &completedTime
	job.Status = string(builder.StatusCompleted)

	// Record build metrics
	metrics.RecordBuild(job.Runtime, "success", completedTime.Sub(startTime).Seconds())

	// Notify: completed
	if err := deps.webhookNotifier.NotifyCompleted(ctx, job.WebhookURL, job.ID); err != nil {
		logger.Error("Failed to send completed webhook", "error", err)
	}

	return nil
}

func handleBuildFailure(
	ctx context.Context,
	job *builder.BuildJob,
	webhookNotifier *builder.WebhookNotifier,
	errorMsg string,
) error {
	logger.Error("Build job failed",
		"job_id", job.ID,
		"function_id", job.FunctionID,
		"error", errorMsg,
	)

	failedTime := time.Now()
	job.CompletedAt = &failedTime
	job.Status = string(builder.StatusFailed)
	job.Error = errorMsg

	// Notify: failed
	if err := webhookNotifier.NotifyFailed(ctx, job.WebhookURL, job.ID, errorMsg); err != nil {
		logger.Error("Failed to send failed webhook", "error", err)
	}

	return fmt.Errorf("%s", errorMsg)
}

func handleBuildFailureWithMetrics(
	ctx context.Context,
	job *builder.BuildJob,
	webhookNotifier *builder.WebhookNotifier,
	errorMsg string,
	startTime time.Time,
) error {
	metrics.RecordBuild(job.Runtime, "failed", time.Since(startTime).Seconds())
	return handleBuildFailure(ctx, job, webhookNotifier, errorMsg)
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
