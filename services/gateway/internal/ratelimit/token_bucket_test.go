package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
)

func TestTokenBucketLimiter_Allow(t *testing.T) {
	// Setup mock Redis server
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	defer mr.Close()

	// Create limiter with mock Redis
	limiter, err := ratelimit.NewTokenBucketLimiter(mr.Addr(), 10, 2)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}
	defer limiter.Close()

	ctx := context.Background()

	t.Run("First request should be allowed", func(t *testing.T) {
		allowed, err := limiter.Allow(ctx, "test-key-1")
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !allowed {
			t.Error("Expected first request to be allowed")
		}
	})

	t.Run("Requests within capacity should be allowed", func(t *testing.T) {
		key := "test-key-2"

		// Should allow 10 requests (capacity)
		for i := 0; i < 10; i++ {
			allowed, err := limiter.Allow(ctx, key)
			if err != nil {
				t.Fatalf("Allow failed on request %d: %v", i, err)
			}
			if !allowed {
				t.Errorf("Request %d should be allowed", i)
			}
		}

		// 11th request should be rate limited
		allowed, err := limiter.Allow(ctx, key)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if allowed {
			t.Error("Expected 11th request to be rate limited")
		}
	})

	t.Run("Tokens should refill over time", func(t *testing.T) {
		key := "test-key-3"

		// Exhaust tokens
		for i := 0; i < 10; i++ {
			limiter.Allow(ctx, key)
		}

		// Should be rate limited
		allowed, err := limiter.Allow(ctx, key)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if allowed {
			t.Error("Expected request to be rate limited")
		}

		// Fast-forward time in miniredis
		mr.FastForward(1 * time.Second)

		// Should have 2 new tokens (refill rate = 2/sec)
		allowed, err = limiter.Allow(ctx, key)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !allowed {
			t.Error("Expected request to be allowed after refill")
		}
	})

	t.Run("Different keys should have independent buckets", func(t *testing.T) {
		key1 := "test-key-4"
		key2 := "test-key-5"

		// Exhaust tokens for key1
		for i := 0; i < 10; i++ {
			limiter.Allow(ctx, key1)
		}

		// key1 should be rate limited
		allowed, err := limiter.Allow(ctx, key1)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if allowed {
			t.Error("Expected key1 to be rate limited")
		}

		// key2 should still be allowed
		allowed, err = limiter.Allow(ctx, key2)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !allowed {
			t.Error("Expected key2 to be allowed")
		}
	})
}

func TestTokenBucketLimiter_GetTokens(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	defer mr.Close()

	limiter, err := ratelimit.NewTokenBucketLimiter(mr.Addr(), 10, 2)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}
	defer limiter.Close()

	ctx := context.Background()
	key := "test-key"

	// Initial tokens should be at capacity
	tokens, err := limiter.GetTokens(ctx, key)
	if err != nil {
		t.Fatalf("GetTokens failed: %v", err)
	}
	if tokens != 10 {
		t.Errorf("Expected 10 tokens, got %d", tokens)
	}

	// Consume 5 tokens
	for i := 0; i < 5; i++ {
		limiter.Allow(ctx, key)
	}

	tokens, err = limiter.GetTokens(ctx, key)
	if err != nil {
		t.Fatalf("GetTokens failed: %v", err)
	}
	if tokens != 5 {
		t.Errorf("Expected 5 tokens, got %d", tokens)
	}
}

func TestTokenBucketLimiter_Reset(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	defer mr.Close()

	limiter, err := ratelimit.NewTokenBucketLimiter(mr.Addr(), 10, 2)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}
	defer limiter.Close()

	ctx := context.Background()
	key := "test-key"

	// Exhaust tokens
	for i := 0; i < 10; i++ {
		limiter.Allow(ctx, key)
	}

	// Should be rate limited
	allowed, err := limiter.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}
	if allowed {
		t.Error("Expected to be rate limited")
	}

	// Reset
	err = limiter.Reset(ctx, key)
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Should be allowed again
	allowed, err = limiter.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}
	if !allowed {
		t.Error("Expected to be allowed after reset")
	}
}
