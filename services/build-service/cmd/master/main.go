package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/builder"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/queue"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/jagjeet-singh-23/mini-lambda/shared/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const QueueName = "build-jobs"

func main() {

	log.Println("🚀 Build Service Master starting...")

	// Configuration
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	s3Endpoint := getEnv("S3_ENDPOINT", "")
	s3Region := getEnv("S3_REGION", "us-east-1")
	s3Bucket := getEnv("S3_BUCKET", "mini-lambda")
	s3AccessKey := getEnv("S3_ACCESS_KEY", "")
	s3SecretKey := getEnv("S3_SECRET_KEY", "")

	// Initialize RabbitMQ publisher
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

	// Initialize webhook notifier
	webhookNotifier := builder.NewWebhookNotifier()

	// Initialize backpressure manager
	backpressureManager := queue.NewBackpressureManager(publisher.GetConnection(), QueueName)

	// Start monitoring queue depth
	// using a background context that will be cancelled when the main function exits
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defer monitorCancel()
	go backpressureManager.MonitorQueueDepth(monitorCtx)

	// Create HTTP server
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Create function endpoint
	mux.HandleFunc("/functions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		handleCreateFunction(w, r, publisher, s3Storage, webhookNotifier, backpressureManager)
	})

	port := getEnv("PORT", "8082")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      middleware.MetricsMiddleware("build-service-master")(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("🛑 Shutting down build master...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("❌ Server shutdown error: %v", err)
		}
	}()

	log.Printf("✅ Build Service Master listening on :%s", port)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("❌ Server error: %v", err)
	}

	log.Println("✅ Build master stopped gracefully")
}

func handleCreateFunction(
	w http.ResponseWriter,
	r *http.Request,
	publisher *queue.Publisher,
	s3Storage *storage.S3Storage,
	webhookNotifier *builder.WebhookNotifier,
	backpressureManager *queue.BackpressureManager,
) {
	ctx := r.Context()

	// Check backpressure
	canAccept, msg, err := backpressureManager.CanAcceptRequest(ctx)
	if err != nil {
		logger.Error("Failed to check backpressure", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if !canAccept {
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}

	// Parse request
	var req builder.CreateFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Name == "" || req.Runtime == "" || (len(req.PackageData) == 0 && req.RepoURL == "") {
		http.Error(w, "Missing required fields: Name, Runtime, and either PackageData or RepoURL", http.StatusBadRequest)
		return
	}

	// Generate Idempotency Key (SHA256 of Name + Runtime + Package content or RepoURL)
	var hashInput string
	if len(req.PackageData) > 0 {
		hashInput = fmt.Sprintf("%s:%s:%s", req.Name, req.Runtime, string(req.PackageData))
	} else {
		hashInput = fmt.Sprintf("%s:%s:%s:%s", req.Name, req.Runtime, req.RepoURL, req.Dockerfile)
	}
	hasher := sha256.New()
	hasher.Write([]byte(hashInput))
	requestHash := hex.EncodeToString(hasher.Sum(nil))

	// Check for existing build (Idempotency)
	existingMetadata, exists, err := s3Storage.CheckBuildMetadata(ctx, requestHash)
	if err != nil {
		logger.Error("Failed to check build metadata", "error", err)
		// Proceeding cautiously - if we can't check, we might want to fail or just build again
		// For now, let's log and proceed, effectively failing open
	}

	if exists && existingMetadata != nil {
		logger.Info("Duplicate build request detected", "hash", requestHash, "function_id", existingMetadata.FunctionID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict) // 409 Conflict
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "conflict",
			"message":     "Function with this configuration and code already exists",
			"function_id": existingMetadata.FunctionID,
			"job_id":      existingMetadata.JobID,
			"created_at":  existingMetadata.CreatedAt,
		})
		return
	}

	// Generate IDs
	functionID, _ := uuid.NewV4()
	jobID, _ := uuid.NewV4()

	logger.Info("Creating function",
		"function_id", functionID.String(),
		"job_id", jobID.String(),
		"name", req.Name,
		"runtime", req.Runtime,
	)

	// Upload package to S3 (only if PackageData is provided)
	var packageKey string
	if len(req.PackageData) > 0 {
		var err error
		packageKey, err = s3Storage.UploadPackage(ctx, functionID.String(), req.PackageData)
		if err != nil {
			logger.Error("Failed to upload package", "error", err)
			http.Error(w, "Failed to upload package", http.StatusInternalServerError)
			return
		}
	}

	memoryMB := int64(req.Memory)
	if memoryMB <= 0 {
		memoryMB = 128
	}
	timeoutSecs := req.Timeout
	if timeoutSecs <= 0 {
		timeoutSecs = 30
	}

	// Create build job
	job := builder.BuildJob{
		ID:          jobID.String(),
		FunctionID:  functionID.String(),
		Name:        req.Name,
		Runtime:     req.Runtime,
		Handler:     req.Handler,
		MemoryMB:    memoryMB,
		TimeoutSecs: timeoutSecs,
		PackageURL:  packageKey,
		RepoURL:     req.RepoURL,
		Dockerfile:  req.Dockerfile,
		WebhookURL:  req.WebhookURL,
		Status:      string(builder.StatusQueued),
		CreatedAt:   time.Now(),
	}

	// Publish to RabbitMQ
	if err := publisher.Publish(ctx, QueueName, job); err != nil {
		logger.Error("Failed to publish build job", "error", err)
		http.Error(w, "Failed to queue build job", http.StatusInternalServerError)
		return
	}

	// Notify webhook: queued
	if err := webhookNotifier.NotifyQueued(ctx, req.WebhookURL, jobID.String()); err != nil {
		logger.Error("Failed to send queued webhook", "error", err)
		// Don't fail the request if webhook fails
	}

	// Return 202 Accepted
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"job_id":      jobID.String(),
		"function_id": functionID.String(),
		"status":      "queued",
		"message":     "Build job queued successfully",
	})

	logger.Info("Build job queued successfully",
		"job_id", jobID.String(),
		"function_id", functionID.String(),
	)

	// Save idempotency metadata
	// We do this AFTER queuing (or ideally before, but we need the IDs)
	// If we fail here, the next request will just trigger a new build, which is acceptable
	metadata := builder.BuildMetadata{
		FunctionID: functionID.String(),
		JobID:      jobID.String(),
		CreatedAt:  time.Now(),
		Runtime:    req.Runtime,
		Name:       req.Name,
		Hash:       requestHash,
	}
	if err := s3Storage.SaveBuildMetadata(ctx, requestHash, metadata); err != nil {
		logger.Error("Failed to save build metadata", "error", err)
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
