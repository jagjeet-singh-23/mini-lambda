package pool

import (
	"context"
	"fmt"
	"sync"
)

type Manager struct {
	pools         map[string]ContainerPool
	mu            sync.RWMutex
	defaultConfig PoolConfig
	runtimeImages map[string]string
}

func NewManager() *Manager {
	return &Manager{
		pools:         make(map[string]ContainerPool),
		defaultConfig: DefaultPoolConfig(""),
		runtimeImages: getDefaultRuntimeImages(),
	}
}

func NewManagerWithConfig(defaultConfig PoolConfig) *Manager {
	return &Manager{
		pools:         make(map[string]ContainerPool),
		defaultConfig: defaultConfig,
		runtimeImages: getDefaultRuntimeImages(),
	}
}

func (m *Manager) GetPool(runtime string) (ContainerPool, error) {
	m.mu.RLock()
	pool, exists := m.pools[runtime]
	m.mu.RUnlock()

	if exists {
		return pool, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if pool, exists := m.pools[runtime]; exists {
		return pool, nil
	}

	baseImage, ok := m.runtimeImages[runtime]
	if !ok {
		return nil, fmt.Errorf("unknown runtime: %s", runtime)
	}

	config := m.defaultConfig
	config.Runtime = runtime

	pool, err := NewDockerPool(config, baseImage)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create pool for runtime %s: %w",
			runtime,
			err,
		)
	}

	m.pools[runtime] = pool
	return pool, nil
}

func (m *Manager) WarmUp(ctx context.Context, runtimes []string) error {
	for _, runtime := range runtimes {
		pool, err := m.GetPool(runtime)
		if err != nil {
			return fmt.Errorf("failed to warm up runtime %s: %w", runtime, err)
		}

		config := m.defaultConfig
		for i := 0; i < config.MinSize; i++ {
			_, err := pool.CreateNew(ctx)
			if err != nil {
				return fmt.Errorf(
					"failed to create new container for runtime %s: %w",
					runtime,
					err,
				)
			}
		}
	}
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errors []error

	for runtime, pool := range m.pools {
		if err := pool.Shutdown(ctx); err != nil {
			errors = append(
				errors,
				fmt.Errorf(
					"failed to shutdown pool for runtime %s: %w",
					runtime,
					err,
				),
			)
		}
	}

	m.pools = make(map[string]ContainerPool)

	if len(errors) > 0 {
		return fmt.Errorf("failed to shutdown pools: %v", errors)
	}

	return nil
}

func (m *Manager) GlobalStats() map[string]PoolStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]PoolStats)
	for runtime, pool := range m.pools {
		stats[runtime] = pool.Stats()
	}
	return stats
}

// SetRuntimeImage allows custom Docker images for runtimes
// Useful for custom base images with pre-installed dependencies
func (m *Manager) SetRuntimeImage(runtime string, image string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.runtimeImages[runtime] = image
	return nil
}

func getDefaultRuntimeImages() map[string]string {
	return map[string]string{
		"python3.9":  "python:3.9-slim",
		"python3.10": "python:3.10-slim",
		"python3.11": "python:3.11-slim",
		"python3.12": "python:3.12-slim",
		"nodejs16":   "node:16-alpine",
		"nodejs18":   "node:18-alpine",
		"nodejs20":   "node:20-alpine",
		"nodejs22":   "node:22-alpine",
		"go1.22":     "golang:1.22-alpine",
		"go1.23":     "golang:1.23-alpine",
		"go1.24":     "golang:1.24-alpine",
	}
}
