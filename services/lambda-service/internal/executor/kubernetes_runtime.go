package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
)

// KubernetesRuntime implements the Runtime interface by sending HTTP requests
// to the internal IP of Pods managed by the KubernetesPodPool.
type KubernetesRuntime struct {
	runtimeType      string
	baseImage        string
	Pool             *pool.KubernetesPodPool
	metricsCollector *metrics.MetricsCollector
	httpClient       *http.Client
}

// NewKubernetesRuntime creates a new Kubernetes-based runtime
func NewKubernetesRuntime(
	runtimeType, baseImage string,
	metricsCollector *metrics.MetricsCollector,
) (*KubernetesRuntime, error) {
	if runtimeType == "" || baseImage == "" {
		return nil, fmt.Errorf("runtime type and base image cannot be empty")
	}

	poolConfig := pool.DefaultPoolConfig(runtimeType)
	containerPool, err := pool.NewKubernetesPodPool(poolConfig, baseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes pod pool: %w", err)
	}

	return &KubernetesRuntime{
		runtimeType:      runtimeType,
		baseImage:        baseImage,
		Pool:             containerPool,
		metricsCollector: metricsCollector,
		httpClient: &http.Client{
			Timeout: 30 * time.Second, // Max execution time for lambda
		},
	}, nil
}

func (r *KubernetesRuntime) Execute(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	if err := function.Validate(); err != nil {
		return nil, fmt.Errorf("invalid function: %w", err)
	}

	return r.executeWithPool(ctx, function, input)
}

func (r *KubernetesRuntime) executeWithPool(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	execMetrics := &ExecutionMetrics{}
	totalTimer := NewTimer()

	container, wasWarmStart, err := r.acquireContainer(ctx, execMetrics)
	if err != nil {
		return nil, err
	}

	defer func() {
		r.releaseContainer(ctx, container, execMetrics)
		execMetrics.TotalTime = totalTimer.Elapsed()
		fmt.Println(execMetrics.String())

		// Record metrics
		if r.metricsCollector != nil {
			r.metricsCollector.RecordPoolAcquireTime(
				r.runtimeType,
				execMetrics.PoolAcquireTime,
			)
			r.metricsCollector.RecordCodeExecutionTime(
				r.runtimeType,
				execMetrics.CodeExecutionTime,
			)
			// Record pool stats
			r.metricsCollector.RecordPoolStats(r.runtimeType, r.Pool.Stats())
		}
	}()

	result, err := r.executeHTTPInPod(
		ctx,
		container, // container.Runtime contains the Pod IP in our implementation
		function,
		input,
		execMetrics,
	)
	if err != nil {
		return nil, err
	}

	result.WasWarmStart = wasWarmStart
	return result, nil
}

func (r *KubernetesRuntime) acquireContainer(
	ctx context.Context,
	m *ExecutionMetrics,
) (*pool.Container, bool, error) {
	poolTimer := NewTimer()
	c, err := r.Pool.Acquire(ctx)
	m.PoolAcquireTime = poolTimer.Elapsed()

	if c != nil && err == nil {
		m.WasWarmStart = true
		m.ContainerID = c.ID
		fmt.Printf("🔥 HTTP WARM: Pod %s\n", c.ID)

		if r.metricsCollector != nil {
			r.metricsCollector.RecordWarmStart(r.runtimeType)
		}

		return c, true, nil
	}

	nc, err := r.Pool.CreateNew(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create new pod: %w", err)
	}
	m.WasWarmStart = false
	m.ContainerID = nc.ID
	fmt.Printf("❄️  HTTP COLD: Pod %s (IP: %s)\n", nc.ID, nc.Runtime)

	if r.metricsCollector != nil {
		r.metricsCollector.RecordColdStart(r.runtimeType)
	}

	return nc, false, nil
}

func (r *KubernetesRuntime) releaseContainer(ctx context.Context, c *pool.Container, m *ExecutionMetrics) {
	releaseTimer := NewTimer()
	if err := r.Pool.Release(ctx, c); err != nil {
		fmt.Printf("Failed to release pod %s: %v\n", c.ID, err)
	}
	m.PoolReleaseTime = releaseTimer.Elapsed()
}

func (r *KubernetesRuntime) executeHTTPInPod(
	ctx context.Context,
	container *pool.Container,
	function *domain.Function,
	input []byte,
	m *ExecutionMetrics,
) (*domain.ExecutionResult, error) {
	codeStartTime := time.Now()

	// The Pod IP was stored in the Runtime field of the Container struct during creation
	podIP := container.Runtime
	url := fmt.Sprintf("http://%s:8080/", podIP)

	payload := map[string]string{
		"code":  base64.StdEncoding.EncodeToString(function.Code),
		"input": base64.StdEncoding.EncodeToString(input),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal function payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	m.ExecWaitTime = time.Since(codeStartTime)

	if err != nil {
		return &domain.ExecutionResult{
			Output:     []byte(fmt.Sprintf("HTTP execution error: %v", err)),
			Logs:       []byte{},
			MemoryUsed: function.Memory * 1024 * 1024,
			ExitCode:   1,
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	m.OutputReadTime = time.Since(codeStartTime) - m.ExecWaitTime

	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var jsonResult map[string]interface{}
	outputStr := string(body)

	if err := json.Unmarshal(body, &jsonResult); err == nil {
		if out, ok := jsonResult["output"].(string); ok {
			outputStr = out
		} else if errMessage, ok := jsonResult["error"].(string); ok {
			outputStr = "Error: " + errMessage
		}
	}

	m.CodeExecutionTime = time.Since(codeStartTime)

	exitCode := 0
	if resp.StatusCode != http.StatusOK {
		exitCode = 1
	}

	return &domain.ExecutionResult{
		Output:     []byte(outputStr),
		Logs:       []byte(outputStr), // Returning output as logs since we captured stdout in server
		MemoryUsed: function.Memory * 1024 * 1024,
		ExitCode:   exitCode,
	}, nil
}

func (r *KubernetesRuntime) Cleanup() error {
	ctx := context.Background()
	if r.Pool != nil {
		return r.Pool.Shutdown(ctx)
	}
	return nil
}

func (r *KubernetesRuntime) GetPoolStats() domain.PoolStats {
	if r.Pool != nil {
		return r.Pool.Stats()
	}
	return domain.PoolStats{}
}
