package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/cache"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/events"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/executor"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/invoke"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/registration"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	_ "github.com/lib/pq"
	"github.com/robfig/cron/v3"

	"github.com/jagjeet-singh-23/mini-lambda/shared/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
			Region:          config.S3Region,
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
	poolCfg := pool.PoolConfig{
		MinSize:      config.PoolMinSize,
		MaxSize:      config.PoolMaxSize,
		MaxIdleTime:  config.PoolIdleTTL,
		MaxUseCount:  500,
		TickInterval: 30 * time.Second,
	}

	runtimeManager, err := executor.NewManager(s3Storage, poolCfg)
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

	invokeHandler := invoke.NewHandler(functionService, runtimeManager, 1024*1024)
	http.HandleFunc("/functions/", invokeHandler.HandleInvoke)

	// Metrics endpoint
	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:         ":8081",
		Handler:      middleware.MetricsMiddleware("lambda-service")(http.DefaultServeMux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start function registration consumer (build-service → lambda-service bridge)
	registrationConsumer, err := registration.NewConsumer(config.RabbitMQURL, s3Storage, functionRepo)
	if err != nil {
		log.Fatalf("Failed to initialize registration consumer: %v", err)
	}
	defer registrationConsumer.Close()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := registrationConsumer.Start(ctx); err != nil {
			log.Printf("Registration consumer error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := eventBus.Start(ctx); err != nil {
			log.Printf("Event bus error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := cronScheduler.Start(ctx); err != nil {
			log.Printf("Cron scheduler error: %v", err)
		}
	}()

	runtimeManager.Start(ctx)

	// Start HTTP server
	go func() {
		log.Printf("✅ Lambda Service listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	waitForShutdown(server, cancel, &wg)

	if err := runtimeManager.Shutdown(context.Background()); err != nil {
		log.Printf("Runtime manager shutdown error: %v", err)
	}

	log.Println("👋 Lambda Service shutdown complete")
}

type Config struct {
	PostgresHost string
	PostgresPort string
	PostgresUser string
	PostgresPass string
	PostgresDB   string
	PostgresSSL  string
	S3Endpoint   string
	S3Region     string
	S3AccessKey  string
	S3SecretKey  string
	S3Bucket     string
	RabbitMQURL  string
	RedisAddr    string
	PoolMinSize  int
	PoolMaxSize  int
	PoolIdleTTL  time.Duration
}

func loadConfig() Config {
	return Config{
		PostgresHost: getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort: getEnv("POSTGRES_PORT", "5432"),
		PostgresUser: getEnv("POSTGRES_USER", "postgres"),
		PostgresPass: getEnv("POSTGRES_PASSWORD", "postgres"),
		PostgresDB:   getEnv("POSTGRES_DB", "lambda_service_db"),
		PostgresSSL:  getEnv("POSTGRES_SSLMODE", "disable"),
		S3Endpoint:   getEnv("S3_ENDPOINT", ""),
		S3Region:     getEnv("S3_REGION", "ap-south-1"),
		S3AccessKey:  getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:  getEnv("S3_SECRET_KEY", ""),
		S3Bucket:     getEnv("S3_BUCKET", ""),
		RabbitMQURL:  getEnv("RABBITMQ_URL", ""),
		RedisAddr:    getEnv("REDIS_CACHE_ADDR", ""),
		PoolMinSize:  getEnvInt("POOL_MIN_SIZE", 1),
		PoolMaxSize:  getEnvInt("POOL_MAX_SIZE", 5),
		PoolIdleTTL:  getEnvDuration("POOL_IDLE_TTL", 5*time.Minute),
	}
}

func initDatabase(config Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.PostgresHost,
		config.PostgresPort,
		config.PostgresUser,
		config.PostgresPass,
		config.PostgresDB,
		config.PostgresSSL,
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

func waitForShutdown(server *http.Server, cancel context.CancelFunc, wg *sync.WaitGroup) {
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

	wg.Wait()
	log.Println("✅ All goroutines stopped")
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
