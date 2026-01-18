package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/jagjeet-singh-23/mini-lambda/internal/metrics"
)

// Manager implements the runtimeManager interface
type Manager struct {
	// runtimes maps runtime type to Runtime implementation
	runtimes map[string]domain.Runtime

	//mu protects concurrent access to the runtimes map
	mu sync.RWMutex

	// metricsCollector collects Prometheus metrics
	metricsCollector *metrics.MetricsCollector
}

func NewManager() (*Manager, error) {
	m := &Manager{
		runtimes:         make(map[string]domain.Runtime),
		metricsCollector: metrics.NewMetricsCollector(),
	}

	if err := m.registerDefaultRuntimes(); err != nil {
		return nil, fmt.Errorf("failed to register default runtimes: %w", err)
	}

	return m, nil
}

func (m *Manager) registerDefaultRuntimes() error {
	runtimeConfigs := []struct {
		runtimeType string
		baseImage   string
	}{
		{"python3.9", "python:3.9-slim"},
		{"python3.11", "python:3.11-slim"},
		{"nodejs18", "node:18-slim"},
		{"nodejs20", "node:20-slim"},
		{"go1.21", "golang:1.21-alpine"},
	}

	for _, config := range runtimeConfigs {
		runtime, err := NewDockerRuntime(
			config.runtimeType,
			config.baseImage,
			m.metricsCollector,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to create %s runtime: %w",
				config.runtimeType,
				err,
			)
		}

		if err := m.RegisterRuntime(config.runtimeType, runtime); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) RegisterRuntime(
	runtimeType string,
	runtime domain.Runtime,
) error {
	if runtimeType == "" {
		return fmt.Errorf("runtime type cannot be empty")
	}

	if runtime == nil {
		return fmt.Errorf("runtime cannot be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.runtimes[runtimeType]; exists {
		return fmt.Errorf("runtime %s already registered", runtimeType)
	}

	m.runtimes[runtimeType] = runtime
	return nil
}

func (m *Manager) GetRuntime(runtimeType string) (domain.Runtime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	runtime, exists := m.runtimes[runtimeType]
	if !exists {
		return nil, fmt.Errorf(
			"runtime %s not found. Available runtimes: %v",
			runtimeType,
			m.listRuntimesUnsafe(),
		)
	}

	return runtime, nil
}

func (m *Manager) ListRuntimes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.listRuntimesUnsafe()
}

func (m *Manager) listRuntimesUnsafe() []string {
	runtimes := make([]string, 0, len(m.runtimes))
	for rt := range m.runtimes {
		runtimes = append(runtimes, rt)
	}
	return runtimes
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errors []error

	for runtimeType, runtime := range m.runtimes {
		if err := runtime.Cleanup(); err != nil {
			errors = append(
				errors,
				fmt.Errorf("failed to cleanup %s:%w", runtimeType, err),
			)
		}
	}

	m.runtimes = make(map[string]domain.Runtime)

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}

	return nil
}

func (m *Manager) Execute(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	runtime, err := m.GetRuntime(function.Runtime)
	if err != nil {
		return nil, err
	}

	return runtime.Execute(ctx, function, input)
}

// GetMetricsCollector returns the metrics collector
func (m *Manager) GetMetricsCollector() *metrics.MetricsCollector {
	return m.metricsCollector
}
