package executor

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
)

// Manager implements the runtimeManager interface
type Manager struct {
	// runtimes maps runtime type to Runtime implementation
	runtimes map[string]domain.Runtime

	//mu protects concurrent access to the runtimes map
	mu sync.RWMutex

	// metricsCollector collects Prometheus metrics
	metricsCollector *metrics.MetricsCollector

	// s3Storage is used to dynamically check for ECR Image URIs
	s3Storage *storage.S3Storage

	poolCfg pool.PoolConfig
}

func NewManager(s3Storage *storage.S3Storage, poolCfg pool.PoolConfig) (*Manager, error) {
	m := &Manager{
		runtimes:         make(map[string]domain.Runtime),
		metricsCollector: metrics.NewMetricsCollector(),
		s3Storage:        s3Storage,
		poolCfg:          poolCfg,
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

	inKubernetes := os.Getenv("KUBERNETES_SERVICE_HOST") != ""

	for _, config := range runtimeConfigs {
		var runtime domain.Runtime
		var err error

		if inKubernetes {
			runtime, err = NewKubernetesRuntime(
				config.runtimeType,
				config.baseImage,
				m.metricsCollector,
				m.s3Storage,
			)
		} else {
			runtime, err = NewDockerRuntime(
				config.runtimeType,
				config.baseImage,
				m.metricsCollector,
				m.poolCfg,
			)
		}

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

type startable interface {
	Start(context.Context)
}

// Start propagates the service context to each runtime's pool lifecycle goroutine.
func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, runtime := range m.runtimes {
		if s, ok := runtime.(startable); ok {
			s.Start(ctx)
		}
	}
}
