package cache

import (
	"context"
	"math/rand"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"golang.org/x/sync/singleflight"
)

// FunctionCache is the read/write contract for the function metadata cache.
type FunctionCache interface {
	GetFunction(ctx context.Context, id string) (*domain.Function, error)
	SetFunction(ctx context.Context, fn *domain.Function) error
	SetFunctionWithTTL(ctx context.Context, fn *domain.Function, ttl time.Duration) error
	DeleteFunction(ctx context.Context, id string) error
}

// CachedFunctionRepository wraps a function repository with caching
type CachedFunctionRepository struct {
	repo  domain.FunctionRepository
	cache FunctionCache
	group singleflight.Group
}

// NewCachedFunctionRepository creates a new cached function repository
func NewCachedFunctionRepository(repo domain.FunctionRepository, cache FunctionCache) *CachedFunctionRepository {
	return &CachedFunctionRepository{
		repo:  repo,
		cache: cache,
	}
}

// FindByID retrieves a function by ID with caching.
// Concurrent cache misses for the same id are collapsed into a single DB query
// via singleflight, preventing cache stampedes.
func (r *CachedFunctionRepository) FindByID(ctx context.Context, id string) (*domain.Function, error) {
	if fn, err := r.cache.GetFunction(ctx, id); err != nil {
		logger.Error("Cache error, falling back to DB", "error", err)
	} else if fn != nil {
		logger.Debug("Function retrieved from cache", "function_id", id)
		return fn, nil
	}

	result, err, _ := r.group.Do(id, func() (interface{}, error) {
		fn, err := r.repo.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		go func() {
			if err := r.setWithJitter(context.Background(), fn); err != nil {
				logger.Error("Failed to cache function", "error", err, "function_id", id)
			}
		}()
		return fn, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*domain.Function), nil
}

// setWithJitter stores fn in the cache with a small random TTL offset so that
// entries written in a burst do not all expire at the same instant.
const baseTTL = 5 * time.Minute
const jitterMax = 60 // seconds

func (r *CachedFunctionRepository) setWithJitter(ctx context.Context, fn *domain.Function) error {
	ttl := baseTTL + time.Duration(rand.Intn(jitterMax))*time.Second
	return r.cache.SetFunctionWithTTL(ctx, fn, ttl)
}

// FindByName retrieves a function by name (bypass cache for name lookups)
func (r *CachedFunctionRepository) FindByName(ctx context.Context, name string) (*domain.Function, error) {
	return r.repo.FindByName(ctx, name)
}

// Save creates or updates a function and updates cache
func (r *CachedFunctionRepository) Save(ctx context.Context, fn *domain.Function) error {
	if err := r.repo.Save(ctx, fn); err != nil {
		return err
	}

	// Update cache (async, with jitter to avoid synchronised expiry)
	go func() {
		if err := r.setWithJitter(context.Background(), fn); err != nil {
			logger.Error("Failed to cache function", "error", err, "function_id", fn.ID)
		}
	}()

	return nil
}

// Delete deletes a function and invalidates cache
func (r *CachedFunctionRepository) Delete(ctx context.Context, id string) error {
	if err := r.repo.Delete(ctx, id); err != nil {
		return err
	}

	// Invalidate cache (async)
	go func() {
		if err := r.cache.DeleteFunction(context.Background(), id); err != nil {
			logger.Error("Failed to invalidate cache", "error", err, "function_id", id)
		}
	}()

	return nil
}

// List lists all functions (bypass cache for list operations)
func (r *CachedFunctionRepository) List(ctx context.Context, offset, limit int) ([]*domain.Function, error) {
	return r.repo.List(ctx, offset, limit)
}

// Count returns the total number of functions (bypass cache)
func (r *CachedFunctionRepository) Count(ctx context.Context) (int64, error) {
	return r.repo.Count(ctx)
}

// Exists checks if a function exists (check cache first, then DB)
func (r *CachedFunctionRepository) Exists(ctx context.Context, id string) (bool, error) {
	// Try cache first
	fn, err := r.cache.GetFunction(ctx, id)
	if err == nil && fn != nil {
		return true, nil
	}

	// Fall back to DB
	return r.repo.Exists(ctx, id)
}
