package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
)

// DockerRuntime implements the Runtime interface using Docker.
type DockerRuntime struct {
	runtimeType      string
	baseImage        string
	client           *client.Client
	registry         *PoolRegistry
	metricsCollector *metrics.MetricsCollector
}

type logResult struct {
	data []byte
	err  error
}

// NewDockerRuntime creates a new Docker-based runtime.
// storage is used by the pool registry to download function code when seeding new containers.
func NewDockerRuntime(
	runtimeType, baseImage string,
	metricsCollector *metrics.MetricsCollector,
	poolCfg pool.PoolConfig,
	storage domain.CodeStorage,
) (*DockerRuntime, error) {
	if runtimeType == "" || baseImage == "" {
		return nil, fmt.Errorf("runtime type and base image cannot be empty")
	}
	cli, err := initDockerClient()
	if err != nil {
		return nil, err
	}
	sweepOrphanedContainers(context.Background(), cli)
	return &DockerRuntime{
		runtimeType:      runtimeType,
		baseImage:        baseImage,
		client:           cli,
		registry:         NewPoolRegistry(poolCfg, storage, cli),
		metricsCollector: metricsCollector,
	}, nil
}

func initDockerClient() (*client.Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("docker daemon not accessible: %w", err)
	}
	return cli, nil
}

func (r *DockerRuntime) Execute(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	if err := function.Validate(); err != nil {
		return nil, fmt.Errorf("invalid function: %w", err)
	}
	if err := r.ensureImage(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure image: %w", err)
	}
	return r.executeWithPool(ctx, function, input)
}

func (r *DockerRuntime) executeWithPool(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	m := &ExecutionMetrics{}
	totalTimer := NewTimer()

	c, wasWarmStart, err := r.acquireContainer(ctx, function, m)
	if err != nil {
		return nil, err
	}

	defer func() {
		r.releaseContainer(ctx, function, c, m)
		m.TotalTime = totalTimer.Elapsed()
		fmt.Println(m.String())
		if r.metricsCollector != nil {
			r.metricsCollector.RecordPoolAcquireTime(r.runtimeType, m.PoolAcquireTime)
			r.metricsCollector.RecordCodeExecutionTime(r.runtimeType, m.CodeExecutionTime)
			r.metricsCollector.RecordOutputReadTime(r.runtimeType, m.OutputReadTime)
			if stats, ok := r.registry.PoolStats(function.ID); ok {
				r.metricsCollector.RecordPoolStats(r.runtimeType, stats)
			}
		}
	}()

	result, err := r.executeInPooledContainer(ctx, c.ID, function, input, m)
	if err != nil {
		return nil, err
	}
	result.WasWarmStart = wasWarmStart
	return result, nil
}

func (r *DockerRuntime) acquireContainer(
	ctx context.Context,
	function *domain.Function,
	m *ExecutionMetrics,
) (*pool.Container, bool, error) {
	poolTimer := NewTimer()
	c, err := r.registry.Acquire(ctx, function)
	m.PoolAcquireTime = poolTimer.Elapsed()
	if err != nil {
		return nil, false, fmt.Errorf("failed to acquire container: %w", err)
	}
	wasWarmStart := c.UseCount > 1
	m.WasWarmStart = wasWarmStart
	m.ContainerID = c.ID
	if wasWarmStart {
		fmt.Printf("🔥 WARM: Container %s (reused %dx)\n", c.ID[:12], c.UseCount)
		if r.metricsCollector != nil {
			r.metricsCollector.RecordWarmStart(r.runtimeType)
		}
	} else {
		fmt.Printf("❄️  COLD: Container %s\n", c.ID[:12])
		if r.metricsCollector != nil {
			r.metricsCollector.RecordColdStart(r.runtimeType)
		}
	}
	return c, wasWarmStart, nil
}

func (r *DockerRuntime) releaseContainer(ctx context.Context, function *domain.Function, c *pool.Container, m *ExecutionMetrics) {
	releaseTimer := NewTimer()
	if err := r.registry.Release(ctx, function, c); err != nil {
		fmt.Printf("Failed to release container %s: %v\n", c.ID, err)
	}
	m.PoolReleaseTime = releaseTimer.Elapsed()
}

func (r *DockerRuntime) executeInPooledContainer(
	ctx context.Context,
	containerID string,
	function *domain.Function,
	input []byte,
	m *ExecutionMetrics,
) (*domain.ExecutionResult, error) {
	codeStartTime := time.Now()

	execID, logReader, err := r.startExecInContainer(ctx, containerID, function, input, m)
	if err != nil {
		return nil, err
	}
	logCh := r.startAsyncLogRead(logReader)
	exitCode, err := r.waitForExec(ctx, execID, function.Timeout, m)
	if err != nil {
		return nil, err
	}
	readTimer := NewTimer()
	output, logErr := r.getAsyncLogs(logCh)
	m.OutputReadTime = readTimer.Elapsed()
	if logErr != nil {
		output = fmt.Appendf(nil, "Failed to read logs: %v", logErr)
	}
	m.CodeExecutionTime = time.Since(codeStartTime) - (m.ExecCreateTime + m.ExecAttachTime)

	return r.collectExecResult(output, function, exitCode), nil
}

func (r *DockerRuntime) startExecInContainer(
	ctx context.Context,
	id string,
	f *domain.Function,
	input []byte,
	m *ExecutionMetrics,
) (string, types.HijackedResponse, error) {
	createTimer := NewTimer()
	execConfig := container.ExecOptions{
		Cmd:          r.buildExecutionCommand(f, input),
		AttachStderr: true,
		AttachStdout: true,
	}
	exec, err := r.client.ContainerExecCreate(ctx, id, execConfig)
	m.ExecCreateTime = createTimer.Elapsed()
	if err != nil {
		return "", types.HijackedResponse{}, fmt.Errorf("failed to create exec: %w", err)
	}

	attachTimer := NewTimer()
	resp, err := r.client.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	m.ExecAttachTime = attachTimer.Elapsed()
	if err != nil {
		return "", types.HijackedResponse{}, fmt.Errorf("failed to attach: %w", err)
	}

	startTimer := NewTimer()
	if err := r.client.ContainerExecStart(ctx, exec.ID, container.ExecStartOptions{}); err != nil {
		resp.Close()
		return "", types.HijackedResponse{}, fmt.Errorf("failed to start: %w", err)
	}
	m.ExecStartTime = startTimer.Elapsed()
	return exec.ID, resp, nil
}

func (r *DockerRuntime) startAsyncLogRead(resp types.HijackedResponse) chan logResult {
	resultCh := make(chan logResult, 1)
	go func() {
		defer resp.Close()
		var stdout, stderr bytes.Buffer
		_, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		resultCh <- logResult{data: combined, err: err}
		close(resultCh)
	}()
	return resultCh
}

func (r *DockerRuntime) getAsyncLogs(resultCh chan logResult) ([]byte, error) {
	result, ok := <-resultCh
	if !ok {
		return nil, fmt.Errorf("log channel closed unexpectedly")
	}
	return result.data, result.err
}

func (r *DockerRuntime) waitForExec(
	ctx context.Context,
	execID string,
	timeout time.Duration,
	m *ExecutionMetrics,
) (int, error) {
	waitTimer := NewTimer()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("execution timeout: %w", ctx.Err())
		case <-ticker.C:
			inspect, err := r.client.ContainerExecInspect(ctx, execID)
			if err != nil {
				return 0, fmt.Errorf("failed to inspect exec: %w", err)
			}
			if !inspect.Running {
				m.ExecWaitTime = waitTimer.Elapsed()
				return int(inspect.ExitCode), nil
			}
		}
	}
}

func (r *DockerRuntime) collectExecResult(logs []byte, f *domain.Function, exitCode int) *domain.ExecutionResult {
	return &domain.ExecutionResult{
		Output:     r.extractOutput(logs),
		Logs:       logs,
		MemoryUsed: f.Memory * 1024 * 1024,
		ExitCode:   exitCode,
	}
}

// buildExecutionCommand builds the docker exec command.
// Code is NOT embedded — it is already on disk at /tmp/handler.py (or .js).
// Only the event (base64-encoded) is passed inline.
func (r *DockerRuntime) buildExecutionCommand(function *domain.Function, input []byte) []string {
	encodedInput := base64.StdEncoding.EncodeToString(input)
	switch r.runtimeType {
	case "python3.9", "python3.11":
		return []string{"python3", "-c", r.getPythonExecScript(encodedInput)}
	case "nodejs18", "nodejs20":
		return []string{"node", "-e", r.getNodeExecScript(encodedInput)}
	default:
		return []string{"sh", "/tmp/handler.sh"}
	}
}

func (r *DockerRuntime) getPythonExecScript(encodedInput string) string {
	return fmt.Sprintf(`
import json, sys, base64
exec(open('/tmp/handler.py').read())
event = {}
try:
    data = base64.b64decode('%s').decode('utf-8')
    if data: event = json.loads(data)
except: pass
if 'handler' in dir():
    print(json.dumps(handler(event, {})))
`, encodedInput)
}

func (r *DockerRuntime) getNodeExecScript(encodedInput string) string {
	return fmt.Sprintf(`
const fs = require('fs');
eval(fs.readFileSync('/tmp/handler.js', 'utf-8'));
const event = JSON.parse(Buffer.from('%s', 'base64').toString('utf-8') || '{}');
Promise.resolve(typeof handler === 'function' ? handler(event, {}) : {}).then(r => console.log(JSON.stringify(r)));
`, encodedInput)
}

func (r *DockerRuntime) ensureImage(ctx context.Context) error {
	_, err := r.client.ImageInspect(ctx, r.baseImage)
	if err == nil {
		return nil
	}
	reader, err := r.client.ImagePull(ctx, r.baseImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()
	_, err = io.Copy(io.Discard, reader)
	return err
}

func (r *DockerRuntime) extractOutput(logs []byte) []byte {
	if len(logs) == 0 {
		return []byte("{}")
	}
	lines := bytes.Split(logs, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) > 0 {
			return line
		}
	}
	return logs
}

// Start propagates the service context into the pool registry.
func (r *DockerRuntime) Start(ctx context.Context) {
	r.registry.Start(ctx)
}

// Cleanup implements the Runtime interface.
func (r *DockerRuntime) Cleanup() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := r.registry.Shutdown(shutdownCtx)
	var clientErr error
	if r.client != nil {
		clientErr = r.client.Close()
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	return clientErr
}

// GetPoolStats returns stats for a specific function's pool.
func (r *DockerRuntime) GetPoolStats(functionID string) (domain.PoolStats, bool) {
	return r.registry.PoolStats(functionID)
}

// sweepOrphanedContainers removes containers left running from a previous (possibly
// crashed) process, identified by the mini-lambda.managed label set at creation.
// Called once at startup; non-fatal on error.
func sweepOrphanedContainers(ctx context.Context, docker *client.Client) {
	f := filters.NewArgs(filters.Arg("label", "mini-lambda.managed=true"))
	containers, err := docker.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		return
	}
	for _, c := range containers {
		docker.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
	}
}
