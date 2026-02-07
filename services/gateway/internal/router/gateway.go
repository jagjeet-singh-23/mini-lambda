package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

// ServiceConfig holds configuration for downstream services
type ServiceConfig struct {
	LambdaServiceURL string
	BuildServiceURL  string
	Timeout          time.Duration
}

// Gateway handles routing and rate limiting
type Gateway struct {
	config      ServiceConfig
	rateLimiter *ratelimit.TokenBucketLimiter
	httpClient  *http.Client
}

// NewGateway creates a new API gateway
func NewGateway(config ServiceConfig, rateLimiter *ratelimit.TokenBucketLimiter) *Gateway {
	return &Gateway{
		config:      config,
		rateLimiter: rateLimiter,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// HandleInvoke routes function invocation requests to lambda-service
func (g *Gateway) HandleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		FunctionID string          `json:"function_id"`
		Payload    json.RawMessage `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Rate limiting per function
	allowed, err := g.rateLimiter.Allow(r.Context(), req.FunctionID)
	if err != nil {
		logger.Error("Rate limiter error", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !allowed {
		w.Header().Set("X-RateLimit-Retry-After", "1")
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Forward to lambda-service
	g.forwardRequest(w, r, g.config.LambdaServiceURL+"/invoke")
}

// HandleCreateFunction routes function creation to build-service
func (g *Gateway) HandleCreateFunction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Forward to build-service
	g.forwardRequest(w, r, g.config.BuildServiceURL+"/functions")
}

// HandleGetFunction routes function retrieval to lambda-service
func (g *Gateway) HandleGetFunction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract function ID from path
	functionID := r.URL.Query().Get("id")
	if functionID == "" {
		http.Error(w, "Missing function_id parameter", http.StatusBadRequest)
		return
	}

	// Forward to lambda-service
	url := fmt.Sprintf("%s/functions?id=%s", g.config.LambdaServiceURL, functionID)
	g.forwardRequest(w, r, url)
}

// HandleListFunctions routes function listing to lambda-service
func (g *Gateway) HandleListFunctions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Forward to lambda-service
	g.forwardRequest(w, r, g.config.LambdaServiceURL+"/functions")
}

// HandleHealth performs health checks on downstream services
func (g *Gateway) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	health := map[string]interface{}{
		"gateway": "healthy",
		"services": map[string]string{
			"lambda-service": g.checkServiceHealth(ctx, g.config.LambdaServiceURL+"/health"),
			"build-service":  g.checkServiceHealth(ctx, g.config.BuildServiceURL+"/health"),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// forwardRequest forwards HTTP request to downstream service
func (g *Gateway) forwardRequest(w http.ResponseWriter, r *http.Request, targetURL string) {
	// Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Create new request
	req, err := http.NewRequestWithContext(
		r.Context(),
		r.Method,
		targetURL,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		logger.Error("Failed to create request", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Add gateway headers
	req.Header.Set("X-Forwarded-By", "mini-lambda-gateway")
	req.Header.Set("X-Forwarded-For", r.RemoteAddr)

	// Execute request
	resp, err := g.httpClient.Do(req)
	if err != nil {
		logger.Error("Service unavailable", "service", targetURL, "error", err)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	io.Copy(w, resp.Body)
}

// checkServiceHealth checks if a service is healthy
func (g *Gateway) checkServiceHealth(ctx context.Context, healthURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return "unhealthy"
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "unhealthy"
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return "healthy"
	}

	return "unhealthy"
}
