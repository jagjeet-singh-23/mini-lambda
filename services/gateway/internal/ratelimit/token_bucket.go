package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// TokenBucketLimiter implements token bucket algorithm using Redis
type TokenBucketLimiter struct {
	client       *redis.Client
	capacity     int64         // Maximum tokens in bucket
	refillRate   int64         // Tokens added per second
	refillPeriod time.Duration // How often to refill

	fallbackMu  sync.RWMutex
	fallbackMap map[string]*rate.Limiter
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
		logger.Error("Failed to connect to Redis for Rate Limiter, will use in-memory fallback", "error", err)
	}

	return &TokenBucketLimiter{
		client:       client,
		capacity:     capacity,
		refillRate:   refillRate,
		refillPeriod: time.Second,
		fallbackMap:  make(map[string]*rate.Limiter),
	}, nil
}

// Allow checks if a request is allowed for the given key
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
		local tokens_str = redis.call('GET', bucket_key)
		local last_refill_str = redis.call('GET', timestamp_key)
		
		local tokens = capacity
		if tokens_str then
			tokens = tonumber(tokens_str)
		end
		
		local last_refill = now
		if last_refill_str then
			last_refill = tonumber(last_refill_str)
		end

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
		logger.Error("Redis rate limit check failed, using in-memory fallback", "key", key, "error", err)
		return tb.allowFallback(key), nil
	}

	return result == 1, nil
}

// allowFallback provides in-memory rate limiting when Redis is unavailable
func (tb *TokenBucketLimiter) allowFallback(key string) bool {
	tb.fallbackMu.RLock()
	limiter, exists := tb.fallbackMap[key]
	tb.fallbackMu.RUnlock()

	if !exists {
		tb.fallbackMu.Lock()
		limiter, exists = tb.fallbackMap[key]
		if !exists {
			// Limit is refillRate per second, burst is capacity
			limiter = rate.NewLimiter(rate.Limit(tb.refillRate), int(tb.capacity))
			tb.fallbackMap[key] = limiter
		}
		tb.fallbackMu.Unlock()
	}

	return limiter.Allow()
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
