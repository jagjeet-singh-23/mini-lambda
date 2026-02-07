package cache

import (
	"context"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

// CachedFunctionRepository wraps a function repository with caching
type CachedFunctionRepository struct {
	repo  domain.FunctionRepository
	cache *RedisCache
}

// NewCachedFunctionRepository creates a new cached function repository
func NewCachedFunctionRepository(repo domain.FunctionRepository, cache *RedisCache) *CachedFunctionRepository {
	return &CachedFunctionRepository{
		repo:  repo,
		cache: cache,
	}
}

// FindByID retrieves a function by ID with caching
func (r *CachedFunctionRepository) FindByID(ctx context.Context, id string) (*domain.Function, error) {
	// Try cache first
	fn, err := r.cache.GetFunction(ctx, id)
	if err != nil {
		logger.Error("Cache error, falling back to DB", "error", err)
	} else if fn != nil {
		logger.Debug("Function retrieved from cache", "function_id", id)
		return fn, nil
	}

	// Cache miss - fetch from DB
	fn, err = r.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Store in cache (async, don't block on cache errors)
	go func() {
		if err := r.cache.SetFunction(context.Background(), fn); err != nil {
			logger.Error("Failed to cache function", "error", err, "function_id", id)
		}
	}()

	return fn, nil
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

	// Update cache (async)
	go func() {
		if err := r.cache.SetFunction(context.Background(), fn); err != nil {
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
