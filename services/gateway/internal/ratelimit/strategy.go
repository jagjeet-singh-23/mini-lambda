package ratelimit

import "context"

// RateLimiter defines the common interface for rate limiting strategies.
type RateLimiter interface {
	// Allow checks whether a request associated with a key is permitted.
	Allow(ctx context.Context, key string) (bool, error)
	// Close shuts down any underlying resources (e.g., Redis connections).
	Close() error
}

// BuildRateLimiterAdapter adapts the existing BuildRateLimiter to the RateLimiter interface
type BuildRateLimiterAdapter struct {
	*BuildRateLimiter
}

// Allow implements the RateLimiter interface by discarding the string reason
func (b *BuildRateLimiterAdapter) Allow(ctx context.Context, key string) (bool, error) {
	allowed, _, err := b.BuildRateLimiter.Allow(ctx, key)
	return allowed, err
}
