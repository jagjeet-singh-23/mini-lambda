package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/api"
	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/jagjeet-singh-23/mini-lambda/internal/events"
	"github.com/jagjeet-singh-23/mini-lambda/internal/runtime"
	"github.com/jagjeet-singh-23/mini-lambda/internal/storage"
	_ "github.com/lib/pq"
	"github.com/robfig/cron/v3"
)

func main() {
	log.Println("🚀 Starting Mini-Lambda Event-Driven Platform...")

	// Load configuration from environment
	config := loadConfig()

	// Initialize database
	db, err := initDatabase(config)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize S3 storage
	s3Storage, err := storage.NewS3Storage(
		context.Background(),
		storage.S3Config{
			Endpoint:        config.S3Endpoint,
			AccessKeyID:     config.S3AccessKey,
			SecretAccessKey: config.S3SecretKey,
			Bucket:          config.S3Bucket,
		},
	)
	if err != nil {
		log.Fatalf("Failed to initialize S3 storage: %v", err)
	}

	// Initialize repositories
	functionRepo := storage.NewPostgresFunctionRepository(db)
	cronRepo := storage.NewPostgresCronRepository(db)
	webhookRepo := storage.NewPostgresWebhookRepository(db)
	auditRepo := storage.NewPostgresEventAuditRepository(db)
	dlqRepo := storage.NewPostgresDeadLetterQueue(db)

	// Initialize runtime manager
	runtimeManager, err := runtime.NewManager()
	if err != nil {
		log.Fatalf("Failed to initialize runtime manager: %v", err)
	}

	// Initialize function service
	functionService := domain.NewFunctionService(
		functionRepo,
		s3Storage,
	)

	// Initialize event processor
	eventProcessor := events.NewDefaultEventProcessor(
		functionService,
		runtimeManager,
		auditRepo,
		dlqRepo,
	)

	// Initialize RabbitMQ event bus
	eventBus, err := events.NewRabbitMQEventBus(
		config.RabbitMQURL,
		eventProcessor,
	)
	if err != nil {
		log.Fatalf("Failed to initialize event bus: %v", err)
	}
	defer eventBus.Shutdown(context.Background())

	// Initialize cron scheduler
	cronInstance := cron.New(cron.WithSeconds())
	cronScheduler := events.NewCronScheduler(cronInstance, eventBus, cronRepo)

	// Initialize webhook handler
	webhookHandler := events.NewWebhookHandler(webhookRepo, eventBus)

	// Initialize API handler
	apiHandler := api.NewHandler(runtimeManager, functionService)
	eventHandler := api.NewEventHandler(
		cronScheduler,
		cronRepo,
		webhookHandler,
		webhookRepo,
		dlqRepo,
		auditRepo,
	)

	// Setup Router
	router := api.SetupRouter(apiHandler, eventHandler)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start background services
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start event bus
	go func() {
		if err := eventBus.Start(ctx); err != nil {
			log.Printf("Event bus error: %v", err)
		}
	}()

	// Start cron scheduler
	go func() {
		if err := cronScheduler.Start(ctx); err != nil {
			log.Printf("Cron scheduler error: %v", err)
		}
	}()

	// Start HTTP server
	go func() {
		log.Printf("🌐 Server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	waitForShutdown(server, cancel)

	log.Println("👋 Shutdown complete")
}

// Config holds application configuration
type Config struct {
	PostgresHost string
	PostgresPort string
	PostgresUser string
	PostgresPass string
	PostgresDB   string
	S3Endpoint   string
	S3AccessKey  string
	S3SecretKey  string
	S3Bucket     string
	RabbitMQURL  string
	InstanceID   string
}

// loadConfig loads configuration from environment variables
func loadConfig() Config {
	return Config{
		PostgresHost: getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort: getEnv("POSTGRES_PORT", "5432"),
		PostgresUser: getEnv("POSTGRES_USER", "postgres"),
		PostgresPass: getEnv("POSTGRES_PASSWORD", "postgres"),
		PostgresDB:   getEnv("POSTGRES_DB", "mini_lambda"),
		S3Endpoint:   getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3AccessKey:  getEnv("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:  getEnv("S3_SECRET_KEY", "minioadmin"),
		S3Bucket:     getEnv("S3_BUCKET", "lambda-functions"),
		RabbitMQURL:  getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		InstanceID:   getEnv("INSTANCE_ID", "1"),
	}
}

// initDatabase initializes the database connection
func initDatabase(config Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.PostgresHost,
		config.PostgresPort,
		config.PostgresUser,
		config.PostgresPass,
		config.PostgresDB,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("✅ Database connected")
	return db, nil
}

// waitForShutdown waits for interrupt signal and gracefully shuts down
func waitForShutdown(server *http.Server, cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("🛑 Shutdown signal received, gracefully stopping...")

	// Cancel background services
	cancel()

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}

// getEnv retrieves environment variable with fallback
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
