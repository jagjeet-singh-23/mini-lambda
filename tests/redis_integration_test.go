package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/cache"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

// TestRedisCache_Integration tests Redis cache with real Redis instance
func TestRedisCache_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	redisAddr := "localhost:6379"
	ctx := context.Background()

	redisCache, err := cache.NewRedisCache(redisAddr, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to create Redis cache: %v", err)
	}
	defer redisCache.Close()

	// Clean up before test
	redisCache.Flush(ctx)

	t.Run("Cache miss returns nil", func(t *testing.T) {
		fn, err := redisCache.GetFunction(ctx, "non-existent-id")
		if err != nil {
			t.Fatalf("GetFunction failed: %v", err)
		}
		if fn != nil {
			t.Error("Expected nil for cache miss")
		}
	})

	t.Run("Set and Get function", func(t *testing.T) {
		testFn := &domain.Function{
			ID:      "test-func-123",
			Name:    "test-function",
			Runtime: "python3.9",
			Handler: "index.handler",
			Timeout: 30,
			Memory:  128,
		}

		// Set function
		err := redisCache.SetFunction(ctx, testFn)
		if err != nil {
			t.Fatalf("SetFunction failed: %v", err)
		}

		// Get function
		cached, err := redisCache.GetFunction(ctx, "test-func-123")
		if err != nil {
			t.Fatalf("GetFunction failed: %v", err)
		}

		if cached == nil {
			t.Fatal("Expected function to be cached")
		}

		if cached.ID != testFn.ID || cached.Name != testFn.Name {
			t.Errorf("Cached function mismatch: got %+v, want %+v", cached, testFn)
		}
	})

	t.Run("Delete function from cache", func(t *testing.T) {
		testFn := &domain.Function{
			ID:   "test-func-456",
			Name: "delete-test",
		}

		// Set function
		redisCache.SetFunction(ctx, testFn)

		// Verify it's cached
		cached, _ := redisCache.GetFunction(ctx, "test-func-456")
		if cached == nil {
			t.Fatal("Function should be cached")
		}

		// Delete from cache
		err := redisCache.DeleteFunction(ctx, "test-func-456")
		if err != nil {
			t.Fatalf("DeleteFunction failed: %v", err)
		}

		// Verify it's deleted
		cached, _ = redisCache.GetFunction(ctx, "test-func-456")
		if cached != nil {
			t.Error("Function should be deleted from cache")
		}
	})

	t.Run("Invocation result caching", func(t *testing.T) {
		invocationID := "inv-123"
		result := []byte(`{"status": "success", "output": "Hello, World!"}`)

		// Set invocation result
		err := redisCache.SetInvocationResult(ctx, invocationID, result, 1*time.Minute)
		if err != nil {
			t.Fatalf("SetInvocationResult failed: %v", err)
		}

		// Get invocation result
		cached, err := redisCache.GetInvocationResult(ctx, invocationID)
		if err != nil {
			t.Fatalf("GetInvocationResult failed: %v", err)
		}

		if string(cached) != string(result) {
			t.Errorf("Cached result mismatch: got %s, want %s", cached, result)
		}
	})

	t.Run("TTL expiration", func(t *testing.T) {
		testFn := &domain.Function{
			ID:   "test-func-ttl",
			Name: "ttl-test",
		}

		// Create cache with short TTL
		shortCache, err := cache.NewRedisCache(redisAddr, 1*time.Second)
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		defer shortCache.Close()

		// Set function
		shortCache.SetFunction(ctx, testFn)

		// Should be cached immediately
		cached, _ := shortCache.GetFunction(ctx, "test-func-ttl")
		if cached == nil {
			t.Error("Function should be cached")
		}

		// Wait for TTL to expire
		time.Sleep(2 * time.Second)

		// Should be expired
		cached, _ = shortCache.GetFunction(ctx, "test-func-ttl")
		if cached != nil {
			t.Error("Function should be expired from cache")
		}
	})
}
