package builder

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jagjeet-singh-23/mini-lambda/shared/buildlog"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/redis/go-redis/v9"
)

type DockerBuilder struct {
	workDir     string
	ecrRepo     string
	redisClient *redis.Client
}

func NewDockerBuilder(workDir, ecrRepo string, redisClient *redis.Client) *DockerBuilder {
	return &DockerBuilder{
		workDir:     workDir,
		ecrRepo:     ecrRepo,
		redisClient: redisClient,
	}
}

// CloneAndBuild clones a GitHub repo, builds the Docker image, pushes to ECR, and returns the Image URI
func (d *DockerBuilder) CloneAndBuild(ctx context.Context, jobID, functionID, repoURL, dockerfilePath string) (string, error) {
	// 1. Create a job-specific working directory
	jobDir := filepath.Join(d.workDir, jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create job directory: %w", err)
	}
	defer os.RemoveAll(jobDir) // Clean up after build

	// 2. Clone the repository
	d.streamLog(ctx, jobID, fmt.Sprintf("Cloning repository: %s", repoURL))
	err := d.runCommandStreaming(ctx, jobID, jobDir, "git", "clone", repoURL, ".")
	if err != nil {
		return "", fmt.Errorf("git clone failed: %w", err)
	}

	// 3. Determine Image URI
	imageURI := fmt.Sprintf("%s:%s", d.ecrRepo, functionID)
	if d.ecrRepo == "" {
		// Fallback for local testing if ECR isn't set yet (e.g. mini-lambda-local:funcID)
		imageURI = fmt.Sprintf("mini-lambda-local:%s", functionID)
	}

	// 4. Build Docker image
	buildCtxPath := jobDir
	actualDockerfile := filepath.Join(jobDir, dockerfilePath)
	if dockerfilePath == "" {
		actualDockerfile = filepath.Join(jobDir, "Dockerfile")
	}

	d.streamLog(ctx, jobID, fmt.Sprintf("Building Docker image: %s using %s", imageURI, actualDockerfile))
	err = d.runCommandStreaming(ctx, jobID, jobDir, "docker", "build", "-t", imageURI, "-f", actualDockerfile, buildCtxPath)
	if err != nil {
		return "", fmt.Errorf("docker build failed: %w", err)
	}

	// 5. Push Docker image (only if we have a real ECR repo to push to)
	if d.ecrRepo != "" {
		d.streamLog(ctx, jobID, fmt.Sprintf("Pushing image to ECR: %s", imageURI))
		err = d.runCommandStreaming(ctx, jobID, jobDir, "docker", "push", imageURI)
		if err != nil {
			return "", fmt.Errorf("docker push failed: %w", err)
		}
	} else {
		d.streamLog(ctx, jobID, "Skipping docker push (no ECR registry configured)")
	}

	d.streamLog(ctx, jobID, "Build completed successfully!")
	return imageURI, nil
}

// streamLog writes a log line to the job's Redis stream via shared/buildlog,
// which enforces the same MaxLen cap the worker's zip path uses.
func (d *DockerBuilder) streamLog(ctx context.Context, jobID, message string) {
	if err := buildlog.Append(ctx, d.redisClient, jobID, message); err != nil {
		logger.Warn("streamLog: failed to write to Redis stream", "job_id", jobID, "error", err)
	}
	logger.Info("[BUILDER STREAM]", "job", jobID, "msg", message)
}

// runCommandStreaming executes a shell command and streams its stdout/stderr to Redis
func (d *DockerBuilder) runCommandStreaming(ctx context.Context, jobID, dir, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Stream stdout
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			d.streamLog(ctx, jobID, scanner.Text())
		}
	}()

	// Stream stderr
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			d.streamLog(ctx, jobID, "ERR: "+scanner.Text())
		}
	}()

	return cmd.Wait()
}
