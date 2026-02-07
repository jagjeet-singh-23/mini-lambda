package main

import (
	"context"
	"encoding/json"
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
)

func main() {
	log.Println("🚀 Build Service Master starting...")

	// Configuration
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	s3Endpoint := getEnv("S3_ENDPOINT", "http://localhost:9000")
	s3Region := getEnv("S3_REGION", "us-east-1")
	s3Bucket := getEnv("S3_BUCKET", "mini-lambda")
	s3AccessKey := getEnv("S3_ACCESS_KEY", "minioadmin")
	s3SecretKey := getEnv("S3_SECRET_KEY", "minioadmin")

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

	// Create HTTP server
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	// Create function endpoint
	mux.HandleFunc("/functions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		handleCreateFunction(w, r, publisher, s3Storage, webhookNotifier)
	})

	port := getEnv("PORT", "8082")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
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
) {
	ctx := r.Context()

	// Parse request
	var req builder.CreateFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Name == "" || req.Runtime == "" || len(req.PackageData) == 0 {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
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

	// Upload package to S3
	packageKey, err := s3Storage.UploadPackage(ctx, functionID.String(), req.PackageData)
	if err != nil {
		logger.Error("Failed to upload package", "error", err)
		http.Error(w, "Failed to upload package", http.StatusInternalServerError)
		return
	}

	// Create build job
	job := builder.BuildJob{
		ID:         jobID.String(),
		FunctionID: functionID.String(),
		Runtime:    req.Runtime,
		PackageURL: packageKey,
		WebhookURL: req.WebhookURL,
		Status:     string(builder.StatusQueued),
		CreatedAt:  time.Now(),
	}

	// Publish to RabbitMQ
	if err := publisher.Publish(ctx, "build-jobs", job); err != nil {
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
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
