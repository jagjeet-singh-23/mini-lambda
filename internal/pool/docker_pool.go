package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type DockerPool struct {
	config       PoolConfig
	dockerClient *client.Client
	baseImage    string
	containers   []*Container
	mu           sync.RWMutex
	stats        poolMetrics
	statsMu      sync.Mutex
	createdAt    time.Time
}

type poolMetrics struct {
	coldStarts     int64
	warmStarts     int64
	totalEvictions int64
	totalCreated   int64
}

func NewDockerPool(config PoolConfig, baseImage string) (*DockerPool, error) {
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
		return nil, fmt.Errorf("failed to ping docker daemon: %w", err)
	}

	pool := &DockerPool{
		config:       config,
		dockerClient: cli,
		baseImage:    baseImage,
		containers:   make([]*Container, 0, config.MaxSize),
		createdAt:    time.Now(),
	}

	return pool, nil
}

func (p *DockerPool) Acquire(ctx context.Context) (*Container, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.containers) == 0 {
		return nil, nil
	}

	for i, container := range p.containers {
		if container.State != StateWarm {
			continue
		}

		container.State = StateInUse
		container.LastUsed = time.Now()
		container.UseCount++

		p.moveToEnd(i)

		p.updateStats(func(m *poolMetrics) {
			m.warmStarts++
		})

		return container, nil
	}

	return nil, nil
}

func (p *DockerPool) Release(ctx context.Context, container *Container) error {
	if container == nil {
		return fmt.Errorf("cannot release nil container")
	}

	if err := p.cleanContainer(ctx, container); err != nil {
		return p.removeContainer(ctx, container)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config.MaxUseCount > 0 && container.UseCount >= p.config.MaxUseCount {
		if err := p.removeContainerUnsafe(ctx, container); err != nil {
			return err
		}

		return nil
	}

	container.State = StateWarm
	container.LastUsed = time.Now()

	return nil
}

func (p *DockerPool) CreateNew(ctx context.Context) (*Container, error) {
	p.mu.RLock()
	atCapacity := len(p.containers) >= p.config.MaxSize
	p.mu.RUnlock()

	if atCapacity {
		if err := p.Evict(ctx); err != nil {
			return nil, fmt.Errorf("failed to evict container: %w", err)
		}
	}

	containerID, err := p.createDockerContainer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker container: %w", err)
	}

	newContainer := &Container{
		ID:         containerID,
		Runtime:    p.config.Runtime,
		CreatedAt:  time.Now(),
		LastUsed:   time.Now(),
		UseCount:   0,
		State:      StateWarm,
		MemoryUsed: 0,
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.containers = append(p.containers, newContainer)

	p.updateStats(func(m *poolMetrics) {
		m.coldStarts++
		m.totalCreated++
	})

	return newContainer, nil
}

func (p *DockerPool) Evict(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.containers) == 0 {
		return fmt.Errorf("pool is empty, nothing to evict")
	}

	// Find LRU container
	var lruIndex int
	var oldestTime time.Time = time.Now()

	for i, container := range p.containers {
		if container.State == StateWarm &&
			container.LastUsed.Before(oldestTime) {
			lruIndex = i
			oldestTime = container.LastUsed
		}
	}

	lruContainer := p.containers[lruIndex]
	return p.removeContainerUnsafe(ctx, lruContainer)
}

func (p *DockerPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.containers)
}

func (p *DockerPool) Stats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	p.statsMu.Lock()
	defer p.statsMu.Unlock()

	// Count warm and in-use containers
	var warmCount, inUseCount int
	var totalUseCount int64

	for _, container := range p.containers {
		switch container.State {
		case StateWarm:
			warmCount++
		case StateInUse:
			inUseCount++
		}
		totalUseCount += container.UseCount
	}

	totalRequests := p.stats.coldStarts + p.stats.warmStarts
	var hitRate float64
	if totalRequests > 0 {
		hitRate = float64(p.stats.warmStarts) / float64(totalRequests) * 100
	}

	var avgUseCount float64
	if len(p.containers) > 0 {
		avgUseCount = float64(totalUseCount) / float64(len(p.containers))
	}

	return PoolStats{
		Runtime:         p.config.Runtime,
		TotalContainers: len(p.containers),
		WarmContainers:  warmCount,
		InUseContainers: inUseCount,
		HitRate:         hitRate,
		ColdStarts:      p.stats.coldStarts,
		WarmStarts:      p.stats.warmStarts,
		TotalEvictions:  p.stats.totalEvictions,
		AverageUseCount: avgUseCount,
		CreatedAt:       p.createdAt,
	}
}

func (p *DockerPool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errors []error

	for _, container := range p.containers {
		if err := p.stopDockerContainer(ctx, container.ID); err != nil {
			errors = append(errors, err)
		}
	}

	p.containers = nil
	if len(errors) > 0 {
		return fmt.Errorf("failed to stop containers: %v", errors)
	}

	return nil
}

func (p *DockerPool) moveToEnd(index int) {
	if index == len(p.containers)-1 {
		return
	}

	container := p.containers[index]
	copy(p.containers[index:], p.containers[index+1:])
	p.containers[len(p.containers)-1] = container
}

func (p *DockerPool) cleanContainer(
	ctx context.Context,
	container *Container,
) error {
	container.State = StateCleaning

	switch p.config.CleanupStrategy {
	case CleanupMinimal:
		return nil

	case CleanupStandard:
		return nil

	case CleanupAggressive:
		if err := p.stopDockerContainer(ctx, container.ID); err != nil {
			return err
		}
		if err := p.startDockerContainer(ctx, container.ID); err != nil {
			return err
		}
		return nil

	default:
		return nil
	}
}

func (p *DockerPool) removeContainer(
	ctx context.Context,
	container *Container,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.removeContainerUnsafe(ctx, container)
}

func (p *DockerPool) removeContainerUnsafe(
	ctx context.Context,
	container *Container,
) error {
	if err := p.stopDockerContainer(ctx, container.ID); err != nil {
		return err
	}

	for i, c := range p.containers {
		if c.ID == container.ID {
			p.containers[i] = p.containers[len(p.containers)-1]
			p.containers = p.containers[:len(p.containers)-1]
			break
		}
	}

	p.updateStats(func(m *poolMetrics) {
		m.totalEvictions++
	})

	return nil
}

// createDockerContainer creates a new Docker container
func (p *DockerPool) createDockerContainer(
	ctx context.Context,
) (string, error) {
	// Container configuration
	config := &container.Config{
		Image:      p.baseImage,
		Tty:        false,
		OpenStdin:  false,
		WorkingDir: "/tmp",
		// Keep container alive - we'll exec commands in it
		Cmd: []string{"tail", "-f", "/dev/null"},
	}

	// Host configuration with resource limits
	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Memory:    128 * 1024 * 1024, // 128MB
			CPUShares: 1024,              // 1 CPU
		},
		NetworkMode: "none", // No network access
		AutoRemove:  false,  // We manage lifecycle
	}

	// Create container
	resp, err := p.dockerClient.ContainerCreate(
		ctx,
		config,
		hostConfig,
		nil, // NetworkingConfig
		nil, // Platform
		"",  // Container name (auto-generated)
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := p.dockerClient.ContainerStart(
		ctx,
		resp.ID,
		container.StartOptions{},
	); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	return resp.ID, nil
}

// startDockerContainer starts a stopped container
func (p *DockerPool) startDockerContainer(
	ctx context.Context,
	containerID string,
) error {
	return p.dockerClient.ContainerStart(
		ctx,
		containerID,
		container.StartOptions{},
	)
}

// stopDockerContainer stops and removes a container
func (p *DockerPool) stopDockerContainer(
	ctx context.Context,
	containerID string,
) error {
	timeout := 5
	_ = p.dockerClient.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	})

	return p.dockerClient.ContainerRemove(
		ctx,
		containerID,
		container.RemoveOptions{
			Force: true,
		},
	)
}

// updateStats safely updates pool metrics
func (p *DockerPool) updateStats(fn func(*poolMetrics)) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	fn(&p.stats)
}
