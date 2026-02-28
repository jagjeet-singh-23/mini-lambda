package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/circuitbreaker"
	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/router"
	"github.com/jagjeet-singh-23/mini-lambda/shared/middleware"
	"github.com/redis/go-redis/v9"
)

func main() {
	log.Println("🚀 API Gateway starting...")

	// Configuration from environment variables
	config := router.ServiceConfig{
		LambdaServiceURL: getEnv("LAMBDA_SERVICE_URL", "http://localhost:8081"),
		BuildServiceURL:  getEnv("BUILD_SERVICE_URL", "http://localhost:8082"),
		Timeout:          30 * time.Second,
	}

	redisAddr := getEnv("REDIS_RATELIMIT_ADDR", "localhost:6379")

	// Initialize rate limiter strategies
	invokeLimiter, buildAdapter := setupRateLimiters(redisAddr)
	defer invokeLimiter.Close()
	defer buildAdapter.Close()

	// Initialize circuit breaker registry
	cbRegistry := setupCircuitBreakers()

	// Initialize Redis stream client
	redisStreamAddr := getEnv("REDIS_CACHE_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisStreamAddr,
	})
	defer redisClient.Close()
	log.Printf("✅ Redis streaming client initialized")

	// Initialize gateway
	gateway := router.NewGateway(config, invokeLimiter, buildAdapter, cbRegistry, redisClient)

	// Setup HTTP routes
	mux := gateway.SetupRoutes()

	// HTTP server
	port := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      middleware.TraceMiddleware(middleware.MetricsMiddleware("gateway")(middleware.LoggingMiddleware(mux))),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 45 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("🛑 Shutting down gateway...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("❌ Server shutdown error: %v", err)
		}
	}()

	log.Printf("✅ API Gateway listening on :%s", port)
	log.Printf("   → Lambda Service: %s", config.LambdaServiceURL)
	log.Printf("   → Build Service: %s", config.BuildServiceURL)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("❌ Server error: %v", err)
	}

	log.Println("✅ Gateway stopped gracefully")
}

// getEnv gets environment variable with fallback
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// setupRateLimiters initializes and returns the RateLimiter strategies
func setupRateLimiters(redisAddr string) (ratelimit.RateLimiter, ratelimit.RateLimiter) {
	// 1. Invoke Rate Limiter (Token Bucket)
	capacity, err := strconv.ParseInt(getEnv("RATE_LIMIT_CAPACITY", "100"), 10, 64)
	if err != nil {
		capacity = 100
		log.Printf("⚠️ Invalid RATE_LIMIT_CAPACITY, defaulting to %d", capacity)
	}
	refillRate, err := strconv.ParseInt(getEnv("RATE_LIMIT_REFILL_RATE", "10"), 10, 64)
	if err != nil {
		refillRate = 10
		log.Printf("⚠️ Invalid RATE_LIMIT_REFILL_RATE, defaulting to %d", refillRate)
	}

	invokeLimiter, err := ratelimit.NewTokenBucketLimiter(redisAddr, capacity, refillRate)
	if err != nil {
		log.Fatalf("❌ Failed to initialize invoke rate limiter: %v", err)
	}
	log.Printf("✅ Invoke rate limiter initialized (capacity: %d, refill: %d/s)", capacity, refillRate)

	// 2. Build Rate Limiter (Fixed Window via Adapter)
	buildRedisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       0,
	})
	buildLimiter := ratelimit.NewBuildRateLimiter(buildRedisClient)
	buildAdapter := &ratelimit.BuildRateLimiterAdapter{BuildRateLimiter: buildLimiter}
	log.Printf("✅ Build rate limiter initialized (Globals: 20/m, User: 5/m)")

	return invokeLimiter, buildAdapter
}

// setupCircuitBreakers initializes the circuit breaker registry
func setupCircuitBreakers() *circuitbreaker.Registry {
	cbRegistry := circuitbreaker.NewRegistry()
	cbRegistry.Register("lambda-service", 10, 5*time.Second)
	cbRegistry.Register("build-service", 5, 30*time.Second)
	log.Printf("✅ Circuit breaker registry initialized (Lambda: 10/5s, Build: 5/30s)")
	return cbRegistry
}
