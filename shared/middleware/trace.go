package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// ContextKey is a custom type for context keys to avoid collisions
type ContextKey string

const (
	// RequestIDKey is the context key for the uniquely generated request ID
	RequestIDKey ContextKey = "requestID"
)

// TraceMiddleware injects a unique X-Request-ID if one does not exist,
// and ensures X-Forwarded-For is properly tracked.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Handle X-Request-ID
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
			r.Header.Set("X-Request-ID", reqID)
		}

		// Set it in the response header so the client knows it
		w.Header().Set("X-Request-ID", reqID)

		// Create a new context with the request ID
		ctx := context.WithValue(r.Context(), RequestIDKey, reqID)
		r = r.WithContext(ctx)

		// 2. Handle X-Forwarded-For
		clientIP := r.RemoteAddr
		xff := r.Header.Get("X-Forwarded-For")
		if xff == "" {
			r.Header.Set("X-Forwarded-For", clientIP)
		} else {
			// Append the current remote address to the chain
			r.Header.Set("X-Forwarded-For", xff+", "+clientIP)
		}

		next.ServeHTTP(w, r)
	})
}

// GetRequestID safely extracts the Request ID from the context.
// Returns an empty string if not found.
func GetRequestID(ctx context.Context) string {
	if reqID, ok := ctx.Value(RequestIDKey).(string); ok {
		return reqID
	}
	return ""
}
