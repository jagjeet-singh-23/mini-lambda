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

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/cache"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/events"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/executor"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	_ "github.com/lib/pq"
	"github.com/robfig/cron/v3"
)

func main() {
	log.Println("🚀 Starting Lambda Service...")

	config := loadConfig()

	// Initialize database (lambda_service_db)
	db, err := initDatabase(config)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize Redis cache
	redisCache, err := cache.NewRedisCache(config.RedisAddr, 5*time.Minute)
	if err != nil {
		log.Fatalf("Failed to initialize Redis cache: %v", err)
	}
	defer redisCache.Close()

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
	baseFunctionRepo := storage.NewPostgresFunctionRepository(db)

	// Wrap with caching layer
	functionRepo := cache.NewCachedFunctionRepository(baseFunctionRepo, redisCache)

	cronRepo := storage.NewPostgresCronRepository(db)
	webhookRepo := storage.NewPostgresWebhookRepository(db)
	auditRepo := storage.NewPostgresEventAuditRepository(db)
	dlqRepo := storage.NewPostgresDeadLetterQueue(db)

	// Initialize runtime/executor
	runtimeManager, err := executor.NewManager()
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

	// Initialize event bus (RabbitMQ)
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

	// Initialize webhook handler (used by cron scheduler and event bus)
	_ = events.NewWebhookHandler(webhookRepo, eventBus)

	// HTTP server for /invoke endpoint
	http.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		// TODO: Implement invoke handler
		// This will be called by the gateway
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Lambda Service - Invoke endpoint"))
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:         ":8081",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

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
		log.Printf("✅ Lambda Service listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	waitForShutdown(server, cancel)

	log.Println("👋 Lambda Service shutdown complete")
}

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
	RedisAddr    string
}

func loadConfig() Config {
	return Config{
		PostgresHost: getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort: getEnv("POSTGRES_PORT", "5432"),
		PostgresUser: getEnv("POSTGRES_USER", "postgres"),
		PostgresPass: getEnv("POSTGRES_PASSWORD", "postgres"),
		PostgresDB:   getEnv("POSTGRES_DB", "lambda_service_db"),
		S3Endpoint:   getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3AccessKey:  getEnv("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:  getEnv("S3_SECRET_KEY", "minioadmin"),
		S3Bucket:     getEnv("S3_BUCKET", "lambda-functions"),
		RabbitMQURL:  getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
	}
}

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

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("✅ Database connected")
	return db, nil
}

func waitForShutdown(server *http.Server, cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("🛑 Shutdown signal received, gracefully stopping...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
