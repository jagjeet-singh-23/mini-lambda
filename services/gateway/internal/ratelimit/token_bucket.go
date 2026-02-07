package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// TokenBucketLimiter implements token bucket algorithm using Redis
type TokenBucketLimiter struct {
	client       *redis.Client
	capacity     int64         // Maximum tokens in bucket
	refillRate   int64         // Tokens added per second
	refillPeriod time.Duration // How often to refill
}

// NewTokenBucketLimiter creates a new token bucket rate limiter
func NewTokenBucketLimiter(redisAddr string, capacity, refillRate int64) (*TokenBucketLimiter, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       0,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &TokenBucketLimiter{
		client:       client,
		capacity:     capacity,
		refillRate:   refillRate,
		refillPeriod: time.Second,
	}, nil
}

// Allow checks if a request is allowed for the given key (e.g., function ID)
// Returns true if allowed, false if rate limited
func (tb *TokenBucketLimiter) Allow(ctx context.Context, key string) (bool, error) {
	bucketKey := fmt.Sprintf("ratelimit:bucket:%s", key)
	timestampKey := fmt.Sprintf("ratelimit:timestamp:%s", key)

	now := time.Now().Unix()

	// Lua script for atomic token bucket operation
	script := redis.NewScript(`
		local bucket_key = KEYS[1]
		local timestamp_key = KEYS[2]
		local capacity = tonumber(ARGV[1])
		local refill_rate = tonumber(ARGV[2])
		local now = tonumber(ARGV[3])

		-- Get current tokens and last refill time
		local tokens = tonumber(redis.call('GET', bucket_key) or capacity)
		local last_refill = tonumber(redis.call('GET', timestamp_key) or now)

		-- Calculate tokens to add based on time elapsed
		local elapsed = now - last_refill
		local tokens_to_add = elapsed * refill_rate
		tokens = math.min(capacity, tokens + tokens_to_add)

		-- Try to consume 1 token
		if tokens == 0 then
			return 0
		end

		tokens = tokens - 1
		redis.call('SET', bucket_key, tokens)
		redis.call('SET', timestamp_key, now)
		redis.call('EXPIRE', bucket_key, 3600)
		redis.call('EXPIRE', timestamp_key, 3600)

		return 1
	`)

	result, err := script.Run(
		ctx,
		tb.client,
		[]string{bucketKey, timestampKey},
		tb.capacity,
		tb.refillRate,
		now,
	).Int()

	if err != nil {
		return false, fmt.Errorf("rate limit check failed: %w", err)
	}

	return result == 1, nil
}

// GetTokens returns the current number of tokens available for a key
func (tb *TokenBucketLimiter) GetTokens(ctx context.Context, key string) (int64, error) {
	bucketKey := fmt.Sprintf("ratelimit:bucket:%s", key)
	timestampKey := fmt.Sprintf("ratelimit:timestamp:%s", key)

	now := time.Now().Unix()

	// Get current tokens
	tokensStr, err := tb.client.Get(ctx, bucketKey).Result()
	if err == redis.Nil {
		return tb.capacity, nil
	}
	if err != nil {
		return 0, err
	}

	var tokens int64
	fmt.Sscanf(tokensStr, "%d", &tokens)

	// Get last refill time
	lastRefillStr, err := tb.client.Get(ctx, timestampKey).Result()
	if err == redis.Nil {
		return tokens, nil
	}
	if err != nil {
		return 0, err
	}

	var lastRefill int64
	fmt.Sscanf(lastRefillStr, "%d", &lastRefill)

	// Calculate current tokens with refill
	elapsed := now - lastRefill
	tokensToAdd := elapsed * tb.refillRate
	currentTokens := min(tb.capacity, tokens+tokensToAdd)

	return currentTokens, nil
}

// Reset clears the rate limit for a specific key
func (tb *TokenBucketLimiter) Reset(ctx context.Context, key string) error {
	bucketKey := fmt.Sprintf("ratelimit:bucket:%s", key)
	timestampKey := fmt.Sprintf("ratelimit:timestamp:%s", key)

	pipe := tb.client.Pipeline()
	pipe.Del(ctx, bucketKey)
	pipe.Del(ctx, timestampKey)
	_, err := pipe.Exec(ctx)

	return err
}

// Close closes the Redis connection
func (tb *TokenBucketLimiter) Close() error {
	return tb.client.Close()
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
