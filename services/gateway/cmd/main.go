package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/router"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

func main() {
	log.Println("🚀 API Gateway starting...")

	// Configuration from environment variables
	config := router.ServiceConfig{
		LambdaServiceURL: getEnv("LAMBDA_SERVICE_URL", "http://localhost:8081"),
		BuildServiceURL:  getEnv("BUILD_SERVICE_URL", "http://localhost:8082"),
		Timeout:          30 * time.Second,
	}

	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")

	// Initialize rate limiter (token bucket)
	// Default: 100 requests per second per function
	capacity := int64(100)
	refillRate := int64(10) // 10 tokens per second

	rateLimiter, err := ratelimit.NewTokenBucketLimiter(redisAddr, capacity, refillRate)
	if err != nil {
		log.Fatalf("❌ Failed to initialize rate limiter: %v", err)
	}
	defer rateLimiter.Close()

	log.Printf("✅ Rate limiter initialized (capacity: %d, refill: %d/s)", capacity, refillRate)

	// Initialize gateway
	gateway := router.NewGateway(config, rateLimiter)

	// Setup HTTP routes
	mux := http.NewServeMux()

	// Function execution
	mux.HandleFunc("/invoke", gateway.HandleInvoke)

	// Function management
	mux.HandleFunc("/functions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			gateway.HandleCreateFunction(w, r)
		case http.MethodGet:
			if r.URL.Query().Get("id") != "" {
				gateway.HandleGetFunction(w, r)
			} else {
				gateway.HandleListFunctions(w, r)
			}
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Health check
	mux.HandleFunc("/health", gateway.HandleHealth)

	// Metrics endpoint (for Prometheus)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "# Gateway metrics\n")
		fmt.Fprintf(w, "gateway_up 1\n")
	})

	// HTTP server
	port := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      loggingMiddleware(mux),
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

// loggingMiddleware logs all incoming requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create response writer wrapper to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)

		logger.Info("Request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// getEnv gets environment variable with fallback
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
