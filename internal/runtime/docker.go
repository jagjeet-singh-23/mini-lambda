package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	//	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
)

// DockerRuntime implements the Runtime interface using Docker
type DockerRuntime struct {
	runtimeType string
	baseImage   string
	client      *client.Client
}

// NewDockerRuntime creates a new Docker-based runtime
func NewDockerRuntime(runtimeType, baseImage string) (*DockerRuntime, error) {
	if runtimeType == "" {
		return nil, fmt.Errorf("runtime type cannot be empty")
	}

	if baseImage == "" {
		return nil, fmt.Errorf("base image cannot be empty")
	}

	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker daemon not accessible: %w", err)
	}

	return &DockerRuntime{
		runtimeType: runtimeType,
		baseImage:   baseImage,
		client:      cli,
	}, nil
}

func (r *DockerRuntime) Execute(
	ctx context.Context,
	function *domain.Function,
	input []byte,
) (*domain.ExecutionResult, error) {
	// Validate function
	if err := function.Validate(); err != nil {
		return nil, fmt.Errorf("invalide function: %w", err)
	}

	// Ensure the base image exists locally
	if err := r.ensureImage(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure image: %w", err)
	}

	containerConfig := r.buildContainerConfig(function, input)
	hostConfig := r.buildHostConfig(function)

	resp, err := r.client.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		nil,
		nil, "",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	containerID := resp.ID

	defer func() {
		cleanupCtx := context.Background()
		r.cleanupContainer(cleanupCtx, containerID)
	}()

	if err := r.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	statusCh, errCh := r.client.ContainerWait(
		ctx,
		containerID,
		container.WaitConditionNotRunning,
	)
	var exitCode int64

	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		exitCode = status.StatusCode
	case <-ctx.Done():
		return nil, fmt.Errorf("execution timeout: %w", ctx.Err())
	}

	logs, err := r.collectLogs(context.Background(), containerID)
	if err != nil {
		logs = fmt.Appendf(nil, "Failed to collect logs: %v", err)
	}

	memoryUsed, err := r.getMemoryUsage(context.Background(), containerID)
	if err != nil {
		memoryUsed = function.Memory * 1024 * 1024
	}

	result := &domain.ExecutionResult{
		Output:     r.extractOutput(logs),
		Logs:       logs,
		MemoryUsed: memoryUsed,
		Duration:   0, // Will be calculated by caller
		ExitCode:   int(exitCode),
	}

	return result, nil
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
	// Base64 encode the code to avoid shell escaping issues
	encodedCode := base64.StdEncoding.EncodeToString(function.Code)

	switch r.runtimeType {
	case "python3.9", "python3.11":
		// Python: decode and execute
		return []string{
			"python3",
			"-c",
			fmt.Sprintf(`
import base64
import sys
import json

# Decode the function code
code = base64.b64decode('%s').decode('utf-8')

# Get input from environment
import os
input_b64 = os.environ.get('LAMBDA_INPUT', '')
if input_b64:
    event = json.loads(base64.b64decode(input_b64).decode('utf-8'))
else:
    event = {}

# Execute the code
exec(code)

# If there's a handler function, call it
if 'handler' in dir():
    result = handler(event, {})
    print(json.dumps(result))
`, encodedCode),
		}

	case "nodejs18", "nodejs20":
		// Node.js: decode and execute
		return []string{
			"node",
			"-e",
			fmt.Sprintf(`
const code = Buffer.from('%s', 'base64').toString('utf-8');
const inputB64 = process.env.LAMBDA_INPUT || '';
let event = {};
if (inputB64) {
    event = JSON.parse(Buffer.from(inputB64, 'base64').toString('utf-8'));
}
eval(code);
if (typeof handler === 'function') {
    handler(event, {}).then(result => {
        console.log(JSON.stringify(result));
    });
}
`, encodedCode),
		}

	default:
		// Fallback: treat as shell script
		return []string{"sh", "-c", string(function.Code)}
	}
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

// collectLogs retrieves stdout and stderr from the container
func (r *DockerRuntime) collectLogs(
	ctx context.Context,
	containerID string,
) ([]byte, error) {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
		Timestamps: false,
	}

	reader, err := r.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// Docker uses a multiplexed stream with 8-byte headers
	// Format: [stream_type, 0, 0, 0, size_byte1, size_byte2, size_byte3, size_byte4, ...data...]
	var output bytes.Buffer
	header := make([]byte, 8)

	for {
		// Read the 8-byte header
		n, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF {
				break
			}
			if n == 0 {
				break
			}
			// Partial read or other error - just break
			break
		}

		// Extract the payload size from bytes 4-7 (big-endian uint32)
		size := uint32(
			header[4],
		)<<24 | uint32(
			header[5],
		)<<16 | uint32(
			header[6],
		)<<8 | uint32(
			header[7],
		)

		// Read the payload
		payload := make([]byte, size)
		_, err = io.ReadFull(reader, payload)
		if err != nil {
			break
		}

		// Append to output
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
