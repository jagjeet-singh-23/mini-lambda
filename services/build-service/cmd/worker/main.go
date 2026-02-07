package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/builder"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/queue"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

func main() {
	log.Println("🚀 Build Service Worker starting...")

	// Configuration
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	s3Endpoint := getEnv("S3_ENDPOINT", "http://localhost:9000")
	s3Region := getEnv("S3_REGION", "us-east-1")
	s3Bucket := getEnv("S3_BUCKET", "mini-lambda")
	s3AccessKey := getEnv("S3_ACCESS_KEY", "minioadmin")
	s3SecretKey := getEnv("S3_SECRET_KEY", "minioadmin")
	workDir := getEnv("WORK_DIR", "/tmp/build-worker")

	// Initialize RabbitMQ consumer
	consumer, err := queue.NewConsumer(amqpURL)
	if err != nil {
		log.Fatalf("❌ Failed to initialize RabbitMQ consumer: %v", err)
	}
	defer consumer.Close()

	log.Println("✅ RabbitMQ consumer initialized")

	// Initialize S3 storage
	s3Storage, err := storage.NewS3Storage(s3Endpoint, s3Region, s3Bucket, s3AccessKey, s3SecretKey)
	if err != nil {
		log.Fatalf("❌ Failed to initialize S3 storage: %v", err)
	}

	log.Println("✅ S3 storage initialized")

	// Initialize ZIP processor
	zipProcessor := builder.NewZIPProcessor(workDir)

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

	// Start consuming build jobs
	err = consumer.Consume("build-jobs", func(body []byte) error {
		return processBuildJob(ctx, body, s3Storage, zipProcessor, webhookNotifier)
	})

	if err != nil {
		log.Fatalf("❌ Consumer error: %v", err)
	}

	log.Println("✅ Build worker stopped gracefully")
}

func processBuildJob(
	ctx context.Context,
	body []byte,
	s3Storage *storage.S3Storage,
	zipProcessor *builder.ZIPProcessor,
	webhookNotifier *builder.WebhookNotifier,
) error {
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
	if err := webhookNotifier.NotifyBuilding(ctx, job.WebhookURL, job.ID); err != nil {
		logger.Error("Failed to send building webhook", "error", err)
	}

	// Download package from S3
	logger.Info("Downloading package from S3", "key", job.PackageURL)
	packageData, err := s3Storage.DownloadPackage(ctx, job.PackageURL)
	if err != nil {
		return handleBuildFailure(ctx, &job, webhookNotifier, fmt.Sprintf("Failed to download package: %v", err))
	}

	// Validate package
	if err := zipProcessor.ValidatePackage(packageData); err != nil {
		return handleBuildFailure(ctx, &job, webhookNotifier, fmt.Sprintf("Invalid package: %v", err))
	}

	// Process ZIP package
	logger.Info("Extracting ZIP package", "function_id", job.FunctionID)
	extractedDir, err := zipProcessor.Process(ctx, packageData, job.FunctionID)
	if err != nil {
		return handleBuildFailure(ctx, &job, webhookNotifier, fmt.Sprintf("Failed to process package: %v", err))
	}
	defer zipProcessor.Cleanup(job.FunctionID)

	// Upload extracted files to S3
	logger.Info("Uploading extracted files to S3", "function_id", job.FunctionID)
	if err := s3Storage.UploadDirectory(ctx, job.FunctionID, extractedDir); err != nil {
		return handleBuildFailure(ctx, &job, webhookNotifier, fmt.Sprintf("Failed to upload artifacts: %v", err))
	}

	// Mark as completed
	completedTime := time.Now()
	job.CompletedAt = &completedTime
	job.Status = string(builder.StatusCompleted)

	logger.Info("Build job completed successfully",
		"job_id", job.ID,
		"function_id", job.FunctionID,
		"duration_ms", completedTime.Sub(startTime).Milliseconds(),
	)

	// Notify: completed
	if err := webhookNotifier.NotifyCompleted(ctx, job.WebhookURL, job.ID); err != nil {
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

	return fmt.Errorf(errorMsg)
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
