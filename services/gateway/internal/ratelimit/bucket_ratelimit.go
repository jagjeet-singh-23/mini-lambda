package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
	"github.com/redis/go-redis/v9"
)

const (
	GlobalBuildLimit  = 20 // 20 build per minute globally
	GlobalBuildWindow = time.Minute

	UserBuildLimit  = 5 // 5 build per minute per user
	UserBuildWindow = time.Minute
)

type BuildRateLimiter struct {
	redis *redis.Client
}

func NewBuildRateLimiter(redisClient *redis.Client) *BuildRateLimiter {
	return &BuildRateLimiter{
		redis: redisClient,
	}
}

// Allow checks if build request is allowed for a user
func (b *BuildRateLimiter) Allow(ctx context.Context, userID string) (bool, string, error) {
	// Check global limit
	globalKey := "ratelimit:builds:global"
	globalAllowed, err := b.checkLimit(ctx, globalKey, GlobalBuildLimit, GlobalBuildWindow)
	if err != nil {
		logger.Error("Failed to check global rate limit")
		return false, "", err
	}

	if !globalAllowed {
		metrics.RateLimitRejectionsTotal.WithLabelValues("build-service", "global").Inc()
		return false, "Global build rate limit exceeded. Please try again later.", nil
	}

	// Check per-user limit
	userKey := "ratelimit:builds:user:" + userID
	userAllowed, err := b.checkLimit(ctx, userKey, UserBuildLimit, UserBuildWindow)
	if err != nil {
		logger.Error("Failed to check user rate limit")
		return false, "", err
	}

	if !userAllowed {
		metrics.RateLimitRejectionsTotal.WithLabelValues("build-service", "user").Inc()
		return false, "User build rate limit exceeded. Please try again later.", nil
	}

	return true, "", nil
}

// Close closes the Redis connection (satisfies RateLimiter interface via adapter)
func (b *BuildRateLimiter) Close() error {
	return b.redis.Close()
}

// checkLimit checks if the given key is within the rate limit
func (b *BuildRateLimiter) checkLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	script := `
	local key = KEYS[1]	
	local limit = tonumber(ARGV[1])
	local window = tonumber(ARGV[2])
	local current = redis.call("INCR", key)

	if current == 1 then
		redis.call("EXPIRE", key, window)
	end

	return current <= limit
	`

	result, err := b.redis.Eval(ctx, script, []string{key}, limit, int(window.Seconds())).Result()
	if err != nil {
		return false, err
	}

	return result.(bool), nil
}

// GetRemainingQuota returns the remaining quota for a user
func (b *BuildRateLimiter) GetRemainingQuota(ctx context.Context, userID string) (int, error) {
	userKey := "ratelimit:builds:user:" + userID
	current, err := b.redis.Get(ctx, userKey).Int()

	if err == redis.Nil {
		return UserBuildLimit, nil
	}

	if err != nil {
		return UserBuildLimit, nil
	}

	remaining := UserBuildLimit - current
	if remaining < 0 {
		remaining = 0
	}

	return remaining, nil
}

// GetResetTime returns when the rate limit resets
func (rl *BuildRateLimiter) GetResetTime(ctx context.Context, userID string) (time.Time, error) {
	userKey := fmt.Sprintf("ratelimit:builds:user:%s", userID)

	ttl, err := rl.redis.TTL(ctx, userKey).Result()
	if err != nil {
		return time.Time{}, err
	}

	return time.Now().Add(ttl), nil
}
