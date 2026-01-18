package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"

	//	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/jagjeet-singh-23/mini-lambda/internal/pool"
)

// DockerRuntime implements the Runtime interface using Docker
type DockerRuntime struct {
	runtimeType string
	baseImage   string
	client      *client.Client
	pool        pool.ContainerPool
	usePooling  bool
}

type logResult struct {
	data []byte
	err  error
}

// NewDockerRuntime creates a new Docker-based runtime
func NewDockerRuntime(runtimeType, baseImage string) (*DockerRuntime, error) {
	if runtimeType == "" || baseImage == "" {
		return nil, fmt.Errorf("runtime type and base image cannot be empty")
	}

	cli, err := initDockerClient()
	if err != nil {
		return nil, err
	}

	containerPool, err := initContainerPool(runtimeType, baseImage)
	if err != nil {
		return nil, err
	}

	return &DockerRuntime{
		runtimeType: runtimeType,
		baseImage:   baseImage,
		client:      cli,
		pool:        containerPool,
		usePooling:  true,
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

func initContainerPool(runtimeType, baseImage string) (pool.ContainerPool, error) {
	poolConfig := pool.DefaultPoolConfig(runtimeType)
	containerPool, err := pool.NewDockerPool(poolConfig, baseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to create container pool: %w", err)
	}
	return containerPool, nil
}

func (r *DockerRuntime) Execute(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	// Validate function
	if err := function.Validate(); err != nil {
		return nil, fmt.Errorf("invalid function: %w", err)
	}

	// Ensure the base image exists locally
	if err := r.ensureImage(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure image: %w", err)
	}

	if r.usePooling {
		return r.executeWithPool(ctx, function, input)
	} else {
		return r.executeWithoutPool(ctx, function, input)
	}
}

func (r *DockerRuntime) executeWithPool(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	metrics := &ExecutionMetrics{}
	totalTimer := NewTimer()

	container, err := r.acquireContainer(ctx, metrics)
	if err != nil {
		return nil, err
	}

	defer func() {
		r.releaseContainer(ctx, container, metrics)
		metrics.TotalTime = totalTimer.Elapsed()
		fmt.Println(metrics.String())
	}()

	return r.executeInPooledContainer(ctx, container.ID, function, input, metrics)
}

func (r *DockerRuntime) acquireContainer(ctx context.Context, m *ExecutionMetrics) (*pool.Container, error) {
	poolTimer := NewTimer()
	c, err := r.pool.Acquire(ctx)
	m.PoolAcquireTime = poolTimer.Elapsed()

	if c != nil && err == nil {
		m.WasWarmStart = true
		m.ContainerID = c.ID
		fmt.Printf("🔥 WARM: Container %s (reused %dx)\n", c.ID[:12], c.UseCount)
		return c, nil
	}

	nc, err := r.pool.CreateNew(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create new container: %w", err)
	}
	m.WasWarmStart = false
	m.ContainerID = nc.ID
	fmt.Printf("❄️  COLD: Container %s (pool size: %d)\n", nc.ID[:12], r.pool.Size())
	return nc, nil
}

func (r *DockerRuntime) releaseContainer(ctx context.Context, c *pool.Container, m *ExecutionMetrics) {
	releaseTimer := NewTimer()
	if err := r.pool.Release(ctx, c); err != nil {
		fmt.Printf("Failed to release container %s: %v\n", c.ID, err)
	}
	m.PoolReleaseTime = releaseTimer.Elapsed()
}

func (r *DockerRuntime) executeWithoutPool(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	containerID, err := r.createAndStartContainer(ctx, function, input)
	if err != nil {
		return nil, err
	}
	defer r.cleanupContainer(context.Background(), containerID)

	exitCode, err := r.waitForContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}

	return r.collectContainerResult(containerID, function, exitCode)
}

func (r *DockerRuntime) createAndStartContainer(
	ctx context.Context,
	f *domain.Function,
	input []byte,
) (string, error) {
	resp, err := r.client.ContainerCreate(
		ctx,
		r.buildContainerConfig(f, input),
		r.buildHostConfig(f),
		nil,
		nil,
		"",
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	if err := r.client.ContainerStart(
		ctx,
		resp.ID,
		container.StartOptions{},
	); err != nil {
		r.cleanupContainer(context.Background(), resp.ID)
		return "", fmt.Errorf("failed to start container: %w", err)
	}
	return resp.ID, nil
}

func (r *DockerRuntime) waitForContainer(ctx context.Context, id string) (int64, error) {
	statusCh, errCh := r.client.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return 0, fmt.Errorf("error waiting for container: %w", err)
		}
		return 0, nil
	case status := <-statusCh:
		return status.StatusCode, nil
	case <-ctx.Done():
		return 0, fmt.Errorf("execution timeout: %w", ctx.Err())
	}
}

func (r *DockerRuntime) collectContainerResult(
	id string,
	f *domain.Function,
	exitCode int64,
) (*domain.ExecutionResult, error) {
	logs, err := r.collectLogs(context.Background(), id)
	if err != nil {
		logs = []byte(fmt.Sprintf("Failed to collect logs: %v", err))
	}

	memoryUsed, err := r.getMemoryUsage(context.Background(), id)
	if err != nil {
		memoryUsed = f.Memory * 1024 * 1024
	}

	return &domain.ExecutionResult{
		Output:     r.extractOutput(logs),
		Logs:       logs,
		MemoryUsed: memoryUsed,
		ExitCode:   int(exitCode),
	}, nil
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
	logCh := r.startAsyncLogRead(ctx, logReader, function.Timeout)
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
		Cmd: r.buildExecutionCommand(f, input), AttachStderr: true, AttachStdout: true,
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

// startAsyncLogRead starts reading logs asynchronously and returns a channel
func (r *DockerRuntime) startAsyncLogRead(
	resp types.HijackedResponse,
) chan logResult {
	resultCh := make(chan logResult, 1)
	go func() {
		defer resp.Close()
		var buf bytes.Buffer
		_, err := io.Copy(&buf, resp.Reader)
		resultCh <- logResult{data: buf.Bytes(), err: err}
		close(resultCh)
	}()
	return resultCh
}

// getAsyncLogs retrieves the result from async log reading
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

func (r *DockerRuntime) collectExecResult(
	logs []byte,
	f *domain.Function,
	exitCode int,
) *domain.ExecutionResult {
	return &domain.ExecutionResult{
		Output:     r.extractOutput(logs),
		Logs:       logs,
		MemoryUsed: f.Memory * 1024 * 1024,
		ExitCode:   exitCode,
	}
}

func (r *DockerRuntime) buildExecutionCommand(
	function *domain.Function,
	input []byte,
) []string {
	encodedInput := base64.StdEncoding.EncodeToString(input)
	encodedCode := base64.StdEncoding.EncodeToString(function.Code)

	switch r.runtimeType {
	case "python3.9", "python3.11":
		return []string{"python3", "-c", r.getPythonExecScript(encodedCode, encodedInput)}
	case "nodejs18", "nodejs20":
		return []string{"node", "-e", r.getNodeExecScript(encodedCode, encodedInput)}
	default:
		return []string{"sh", "-c", string(function.Code)}
	}
}

func (r *DockerRuntime) getPythonExecScript(code, input string) string {
	return fmt.Sprintf(`
import base64, json, sys
code = base64.b64decode('%s').decode('utf-8')
event = {}
try:
    input_data = base64.b64decode('%s').decode('utf-8')
    if input_data: event = json.loads(input_data)
except: pass
exec(code)
if 'handler' in dir():
    print(json.dumps(handler(event, {})))
`, code, input)
}

func (r *DockerRuntime) getNodeExecScript(code, input string) string {
	return fmt.Sprintf(`
const code = Buffer.from('%s', 'base64').toString('utf-8');
let event = {};
try {
    const input = Buffer.from('%s', 'base64').toString('utf-8');
    if (input) event = JSON.parse(input);
} catch(e) {}
eval(code);
if (typeof handler === 'function') {
    Promise.resolve(handler(event, {})).then(res => console.log(JSON.stringify(res)));
}
`, code, input)
}

func (r *DockerRuntime) ensureImage(ctx context.Context) error {
	_, err := r.client.ImageInspect(ctx, r.baseImage)
	if err == nil {
		return nil
	}

	// Image doesn't exists, pull it
	reader, err := r.client.ImagePull(ctx, r.baseImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	defer reader.Close()

	_, err = io.Copy(io.Discard, reader)
	return err
}

func (r *DockerRuntime) buildContainerConfig(
	function *domain.Function,
	input []byte,
) *container.Config {
	return &container.Config{
		Image:        r.baseImage,
		Cmd:          r.buildCommand(function),
		Env:          r.buildEnvironment(function, input),
		WorkingDir:   "/tmp",
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}
}

func (r *DockerRuntime) buildHostConfig(
	function *domain.Function,
) *container.HostConfig {
	memoryLimit := function.Memory * 1024 * 1024
	pidsLimit := int64(256)

	return &container.HostConfig{
		NetworkMode:    "none",
		AutoRemove:     false,
		ReadonlyRootfs: true,
		Resources: container.Resources{
			Memory:    memoryLimit,
			CPUShares: 1024,
			PidsLimit: &pidsLimit,
		},
	}
}

func (r *DockerRuntime) buildCommand(function *domain.Function) []string {
	encodedCode := base64.StdEncoding.EncodeToString(function.Code)

	switch r.runtimeType {
	case "python3.9", "python3.11":
		return []string{"python3", "-c", r.getPythonColdScript(encodedCode)}
	case "nodejs18", "nodejs20":
		return []string{"node", "-e", r.getNodeColdScript(encodedCode)}
	default:
		return []string{"sh", "-c", string(function.Code)}
	}
}

func (r *DockerRuntime) getPythonColdScript(code string) string {
	return fmt.Sprintf(`
import base64, sys, json, os
code = base64.b64decode('%s').decode('utf-8')
input_b64 = os.environ.get('LAMBDA_INPUT', '')
event = json.loads(base64.b64decode(input_b64).decode('utf-8')) if input_b64 else {}
exec(code)
if 'handler' in dir():
    print(json.dumps(handler(event, {})))
`, code)
}

func (r *DockerRuntime) getNodeColdScript(code string) string {
	return fmt.Sprintf(`
const code = Buffer.from('%s', 'base64').toString('utf-8');
const inputB64 = process.env.LAMBDA_INPUT || '';
const event = inputB64 ? JSON.parse(Buffer.from(inputB64, 'base64').toString('utf-8')) : {};
eval(code);
if (typeof handler === 'function') {
    handler(event, {}).then(res => console.log(JSON.stringify(res)));
}
`, code)
}

func (r *DockerRuntime) buildEnvironment(
	function *domain.Function,
	input []byte,
) []string {
	env := []string{
		// AWS Lambda-compatible environment variables
		fmt.Sprintf("AWS_LAMBDA_FUNCTION_NAME=%s", function.Name),
		fmt.Sprintf("AWS_LAMBDA_FUNCTION_MEMORY_SIZE=%d", function.Memory),
		"AWS_LAMBDA_FUNCTION_VERSION=1",
		"AWS_REGION=us-east-1", // Default region

		// Pass input as base64-encoded JSON
		fmt.Sprintf(
			"LAMBDA_INPUT=%s",
			base64.StdEncoding.EncodeToString(input),
		),

		// Set timezone
		"TZ=UTC",
	}

	for key, value := range function.Environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	return env
}

func (r *DockerRuntime) collectLogs(ctx context.Context, id string) ([]byte, error) {
	options := container.LogsOptions{ShowStdout: true, ShowStderr: true}
	reader, err := r.client.ContainerLogs(ctx, id, options)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return r.parseDockerStream(reader)
}

func (r *DockerRuntime) parseDockerStream(reader io.Reader) ([]byte, error) {
	var output bytes.Buffer
	header := make([]byte, 8)

	for {
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		size := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])
		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			break
		}
		output.Write(payload)
	}

	return output.Bytes(), nil
}

// extractOutput extracts just the last line (the function result)
func (r *DockerRuntime) extractOutput(logs []byte) []byte {
	if len(logs) == 0 {
		return []byte("{}")
	}

	// Split by newlines and get the last non-empty line
	lines := bytes.Split(logs, []byte("\n"))

	// Find last non-empty line
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) > 0 {
			return line
		}
	}

	// If no output, return the full logs
	return logs
}

func (r *DockerRuntime) getMemoryUsage(
	ctx context.Context,
	containerID string,
) (int64, error) {
	stats, err := r.client.ContainerStats(ctx, containerID, false)
	if err != nil {
		return 0, err
	}

	defer stats.Body.Close()

	// TODO: Parse stats
	return 0, nil
}

func (r *DockerRuntime) cleanupContainer(
	ctx context.Context,
	containerID string,
) {
	// Stop the container
	timeout := 5
	_ = r.client.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	})

	// Remove the container
	_ = r.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: true,
	})
}

// Cleanup implements the Runtime interface
// Called when shutting down the runtime
func (r *DockerRuntime) Cleanup() error {
	if r.client != nil {
		return r.client.Close()
	}

	return nil
}

// GetPoolStats returns statistics about the container pool
func (r *DockerRuntime) GetPoolStats() pool.PoolStats {
	if r.pool != nil {
		return r.pool.Stats()
	}
	return pool.PoolStats{}
}

// DisablePooling disables container pooling
func (r *DockerRuntime) DisablePooling() {
	r.usePooling = false
}

// EnablePooling enables container pooling
func (r *DockerRuntime) EnablePooling() {
	r.usePooling = true
}
