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
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
	uuid "github.com/satori/go.uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KubernetesRuntime implements the Runtime interface by sending HTTP requests
// to the internal IP of Pods managed by the KubernetesPodPool.
type KubernetesRuntime struct {
	runtimeType      string
	baseImage        string
	Pool             *pool.KubernetesPodPool
	metricsCollector *metrics.MetricsCollector
	httpClient       *http.Client
	s3Storage        *storage.S3Storage
}

// NewKubernetesRuntime creates a new Kubernetes-based runtime
func NewKubernetesRuntime(
	runtimeType, baseImage string,
	metricsCollector *metrics.MetricsCollector,
	s3Storage *storage.S3Storage,
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
		s3Storage:        s3Storage,
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

	// 1. Check if an ECR custom image exists for this function
	if r.s3Storage != nil {
		imageURIData, err := r.s3Storage.RetrieveRaw(ctx, function.ID+"_imageuri")
		if err == nil && len(imageURIData) > 0 {
			imageURI := string(imageURIData)
			return r.executeCustomImage(ctx, function, input, imageURI)
		}
	}

	// 2. Fallback to extracting the Zip dynamically into a generic warm pool runner
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

// executeCustomImage provisions a one-off Kubernetes pod with the exact ECR image, executes it, and tears it down
func (r *KubernetesRuntime) executeCustomImage(
	ctx context.Context,
	function *domain.Function,
	input []byte,
	imageURI string,
) (*domain.ExecutionResult, error) {
	codeStartTime := time.Now()
	execMetrics := &ExecutionMetrics{}

	// Create a unique one-off pod for this native container
	podName := fmt.Sprintf("native-%s-%s", function.ID[:8], uuid.NewV4().String()[:8])
	namespace := "mini-lambda"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":         "lambda-native-runner",
				"function_id": function.ID,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "runner",
					Image: imageURI,
					Ports: []corev1.ContainerPort{
						{ContainerPort: 8080},
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", function.Memory)),
							corev1.ResourceCPU:    resource.MustParse("250m"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("128Mi"),
							corev1.ResourceCPU:    resource.MustParse("100m"),
						},
					},
				},
			},
		},
	}

	// Creating the pod directly via K8s Client (needs to be exposed from pool or initialized here)
	// We'll just construct a temporary container mock to reuse HTTP logic
	// Note: We'd need the k8s clientset here to actually spin up the pod. Since we don't have it directly on ر.KubernetesRuntime,
	// let's grab it from the Pool instance which does.
	clientset := r.Pool.GetClient()

	createdPod, err := clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create custom native pod: %w", err)
	}

	// Ensure cleanup
	defer clientset.CoreV1().Pods(namespace).Delete(context.Background(), createdPod.Name, metav1.DeleteOptions{})

	// Wait for Pod IP
	watcher, err := clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", createdPod.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch native pod: %w", err)
	}
	defer watcher.Stop()

	var podIP string
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

Loop:
	for {
		select {
		case event := <-watcher.ResultChan():
			p, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
				podIP = p.Status.PodIP
				break Loop
			}
			if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodUnknown {
				return nil, fmt.Errorf("native pod failed to start")
			}
		case <-timeoutCtx.Done():
			return nil, fmt.Errorf("timeout waiting for native pod to start")
		}
	}

	// Fake an acquired pool container just to reuse the HTTP execution logic
	mockContainer := &pool.Container{
		ID:      createdPod.Name,
		Runtime: podIP,
	}

	execMetrics.PoolAcquireTime = time.Since(codeStartTime)

	result, err := r.executeHTTPInPod(ctx, mockContainer, function, input, execMetrics)
	if err != nil {
		return nil, err
	}

	result.WasWarmStart = false
	return result, nil
}
