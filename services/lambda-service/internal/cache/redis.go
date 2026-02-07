package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/redis/go-redis/v9"
)

// RedisCache provides caching for function metadata
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCache creates a new Redis cache client
func NewRedisCache(addr string, ttl time.Duration) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Redis cache initialized", "addr", addr, "ttl_seconds", ttl.Seconds())

	return &RedisCache{
		client: client,
		ttl:    ttl,
	}, nil
}

// GetFunction retrieves a function from cache
func (c *RedisCache) GetFunction(ctx context.Context, id string) (*domain.Function, error) {
	key := fmt.Sprintf("function:%s", id)

	data, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		// Cache miss
		logger.Debug("Cache miss", "key", key)
		return nil, nil
	}
	if err != nil {
		logger.Error("Cache get error", "error", err, "key", key)
		return nil, err
	}

	var fn domain.Function
	if err := json.Unmarshal(data, &fn); err != nil {
		logger.Error("Cache unmarshal error", "error", err, "key", key)
		return nil, err
	}

	logger.Debug("Cache hit", "key", key)
	return &fn, nil
}

// SetFunction stores a function in cache
func (c *RedisCache) SetFunction(ctx context.Context, fn *domain.Function) error {
	key := fmt.Sprintf("function:%s", fn.ID)

	data, err := json.Marshal(fn)
	if err != nil {
		return fmt.Errorf("failed to marshal function: %w", err)
	}

	if err := c.client.Set(ctx, key, data, c.ttl).Err(); err != nil {
		logger.Error("Cache set error", "error", err, "key", key)
		return err
	}

	logger.Debug("Cache set", "key", key, "ttl_seconds", c.ttl.Seconds())
	return nil
}

// DeleteFunction removes a function from cache
func (c *RedisCache) DeleteFunction(ctx context.Context, id string) error {
	key := fmt.Sprintf("function:%s", id)

	if err := c.client.Del(ctx, key).Err(); err != nil {
		logger.Error("Cache delete error", "error", err, "key", key)
		return err
	}

	logger.Debug("Cache delete", "key", key)
	return nil
}

// GetInvocationResult retrieves an invocation result from cache
func (c *RedisCache) GetInvocationResult(ctx context.Context, invocationID string) ([]byte, error) {
	key := fmt.Sprintf("invocation:%s", invocationID)

	data, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		logger.Debug("Cache miss", "key", key)
		return nil, nil
	}
	if err != nil {
		logger.Error("Cache get error", "error", err, "key", key)
		return nil, err
	}

	logger.Debug("Cache hit", "key", key)
	return data, nil
}

// SetInvocationResult stores an invocation result in cache
func (c *RedisCache) SetInvocationResult(ctx context.Context, invocationID string, result []byte, ttl time.Duration) error {
	key := fmt.Sprintf("invocation:%s", invocationID)

	if err := c.client.Set(ctx, key, result, ttl).Err(); err != nil {
		logger.Error("Cache set error", "error", err, "key", key)
		return err
	}

	logger.Debug("Cache set", "key", key, "ttl_seconds", ttl.Seconds())
	return nil
}

// GetStats returns cache statistics
func (c *RedisCache) GetStats(ctx context.Context) (map[string]interface{}, error) {
	info, err := c.client.Info(ctx, "stats").Result()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"info": info,
	}, nil
}

// Close closes the Redis connection
func (c *RedisCache) Close() error {
	return c.client.Close()
}

// Flush clears all cache entries (use with caution!)
func (c *RedisCache) Flush(ctx context.Context) error {
	return c.client.FlushDB(ctx).Err()
}
