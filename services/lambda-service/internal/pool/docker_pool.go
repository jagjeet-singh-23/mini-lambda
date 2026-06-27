package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

type DockerPool struct {
	config    PoolConfig
	docker    *client.Client
	baseImage string
	idle      chan *Container // warm containers; cap = MaxSize
	inUse     sync.Map       // containerID → *Container
	stats     poolMetrics
	statsMu   sync.Mutex
	createdAt time.Time
	closed    atomic.Bool   // set to true in Shutdown; guards against send on closed chan
	stopCh    chan struct{}  // closed by Shutdown to stop the lifecycle goroutine
	wg        sync.WaitGroup // tracks the lifecycle goroutine
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

	return &DockerPool{
		config:    config,
		docker:    cli,
		baseImage: baseImage,
		idle:      make(chan *Container, config.MaxSize),
		stopCh:    make(chan struct{}),
		createdAt: time.Now(),
	}, nil
}

// Start pre-fills the pool to MinSize and launches the lifecycle goroutine.
func (p *DockerPool) Start(ctx context.Context) {
	for len(p.idle) < p.config.MinSize {
		if err := p.enqueueNew(ctx); err != nil {
			break
		}
	}
	p.wg.Add(1)
	go p.runLifecycle(ctx)
}

func (p *DockerPool) runLifecycle(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.config.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.evictAndReplenish(ctx)
		}
	}
}

// Acquire blocks until a warm container is available or ctx is cancelled.
func (p *DockerPool) Acquire(ctx context.Context) (*Container, error) {
	select {
	case c := <-p.idle:
		if c == nil {
			return nil, fmt.Errorf("pool is shut down")
		}
		c.State = StateInUse
		c.LastUsed = time.Now()
		c.UseCount++
		p.inUse.Store(c.ID, c)
		p.updateStats(func(m *poolMetrics) { m.warmStarts++ })
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns a container to the idle pool, or retires it if MaxUseCount is reached.
func (p *DockerPool) Release(ctx context.Context, c *Container) error {
	if c == nil {
		return fmt.Errorf("cannot release nil container")
	}
	p.inUse.Delete(c.ID)

	if p.config.MaxUseCount > 0 && c.UseCount >= p.config.MaxUseCount {
		if err := p.stopDockerContainer(ctx, c.ID); err != nil {
			return err
		}
		p.updateStats(func(m *poolMetrics) { m.totalEvictions++ })
		go p.replenishOne(context.Background())
		return nil
	}

	c.State = StateWarm
	c.LastUsed = time.Now()
	select {
	case p.idle <- c:
	default:
		p.stopDockerContainer(ctx, c.ID)
	}
	return nil
}

// CreateNew creates a new container and adds it to the idle pool.
// Satisfies the ContainerPool interface. Returns nil, nil on success.
func (p *DockerPool) CreateNew(ctx context.Context) (*Container, error) {
	return nil, p.enqueueNew(ctx)
}

// Evict removes one warm container from the idle pool.
func (p *DockerPool) Evict(ctx context.Context) error {
	select {
	case c := <-p.idle:
		return p.stopDockerContainer(ctx, c.ID)
	default:
		return fmt.Errorf("pool is empty, nothing to evict")
	}
}

func (p *DockerPool) Size() int {
	inUse := 0
	p.inUse.Range(func(_, _ any) bool { inUse++; return true })
	return len(p.idle) + inUse
}

func (p *DockerPool) Stats() domain.PoolStats {
	p.statsMu.Lock()
	snap := p.stats
	p.statsMu.Unlock()

	inUse := 0
	p.inUse.Range(func(_, _ any) bool { inUse++; return true })
	warm := len(p.idle)

	totalReqs := snap.coldStarts + snap.warmStarts
	var hitRate float64
	if totalReqs > 0 {
		hitRate = float64(snap.warmStarts) / float64(totalReqs) * 100
	}

	return domain.PoolStats{
		Runtime:         p.config.Runtime,
		TotalContainers: warm + inUse,
		WarmContainers:  warm,
		InUseContainers: inUse,
		HitRate:         hitRate,
		ColdStarts:      snap.coldStarts,
		WarmStarts:      snap.warmStarts,
		TotalEvictions:  snap.totalEvictions,
		CreatedAt:       p.createdAt,
	}
}

// Shutdown stops the lifecycle goroutine, then stops and removes all containers.
func (p *DockerPool) Shutdown(ctx context.Context) error {
	// Signal the lifecycle goroutine to stop, wait for it, then close the
	// idle channel. All three are guarded by the CAS so a second Shutdown
	// call cannot double-close either channel.
	if p.closed.CompareAndSwap(false, true) {
		close(p.stopCh)
		p.wg.Wait()
		close(p.idle)
	}
	var errs []error
	for c := range p.idle {
		if err := p.stopDockerContainer(ctx, c.ID); err != nil {
			errs = append(errs, err)
		}
	}
	p.inUse.Range(func(_, v any) bool {
		c := v.(*Container)
		if err := p.stopDockerContainer(ctx, c.ID); err != nil {
			errs = append(errs, err)
		}
		return true
	})
	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}
	return nil
}

// enqueueNew creates a Docker container and places it in the idle channel.
func (p *DockerPool) enqueueNew(ctx context.Context) error {
	if p.closed.Load() {
		return fmt.Errorf("pool is shut down")
	}
	id, err := p.createDockerContainer(ctx)
	if err != nil {
		return fmt.Errorf("create docker container: %w", err)
	}
	c := &Container{
		ID:        id,
		Runtime:   p.config.Runtime,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		State:     StateWarm,
	}
	p.updateStats(func(m *poolMetrics) { m.coldStarts++; m.totalCreated++ })
	if p.closed.Load() {
		// Shutdown raced with us — discard the container we just created.
		p.stopDockerContainer(ctx, id)
		return fmt.Errorf("pool is shut down")
	}
	select {
	case p.idle <- c:
	default:
		// Pool is full (race between enqueueNew calls); discard this one.
		p.stopDockerContainer(ctx, id)
	}
	return nil
}

// replenishOne creates one container if pool is below MinSize.
// Called from Release when a container is retired.
func (p *DockerPool) replenishOne(ctx context.Context) {
	if len(p.idle) >= p.config.MinSize {
		return
	}
	_ = p.enqueueNew(ctx)
}

// evictAndReplenish drains the idle channel, discards stale containers,
// puts fresh ones back, then fills to MinSize.
// Stopping old containers and creating new ones happen in background goroutines
// so the ticker loop is never blocked by Docker API calls.
func (p *DockerPool) evictAndReplenish(ctx context.Context) {
	var keep []*Container
	var toEvict []string
	for {
		select {
		case c := <-p.idle:
			if time.Since(c.LastUsed) < p.config.MaxIdleTime {
				keep = append(keep, c)
			} else {
				toEvict = append(toEvict, c.ID)
				p.updateStats(func(m *poolMetrics) { m.totalEvictions++ })
			}
		default:
			goto done
		}
	}
done:
	// Stop evicted containers in the background.
	for _, id := range toEvict {
		id := id
		go p.stopDockerContainer(ctx, id)
	}
	// Return kept containers to the idle channel.
	for _, c := range keep {
		select {
		case p.idle <- c:
		default:
			go p.stopDockerContainer(ctx, c.ID)
		}
	}
	// Replenish to MinSize in the background so the ticker is not blocked.
	deficit := p.config.MinSize - len(p.idle)
	for i := 0; i < deficit; i++ {
		go p.enqueueNew(ctx)
	}
}

func (p *DockerPool) createDockerContainer(ctx context.Context) (string, error) {
	cfg := &container.Config{
		Image:      p.baseImage,
		WorkingDir: "/tmp",
		Cmd:        []string{"tail", "-f", "/dev/null"},
	}
	hostCfg := &container.HostConfig{
		Resources: container.Resources{
			Memory:    128 * 1024 * 1024,
			CPUShares: 1024,
		},
		NetworkMode:    "none",
		AutoRemove:     false,
		ReadonlyRootfs: true,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeTmpfs,
				Target: "/tmp",
				TmpfsOptions: &mount.TmpfsOptions{
					SizeBytes: 64 * 1024 * 1024,
				},
			},
		},
	}
	resp, err := p.docker.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("ContainerCreate: %w", err)
	}
	if err := p.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("ContainerStart: %w", err)
	}
	return resp.ID, nil
}

func (p *DockerPool) stopDockerContainer(ctx context.Context, id string) error {
	timeout := 5
	_ = p.docker.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout})
	return p.docker.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

func (p *DockerPool) updateStats(fn func(*poolMetrics)) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	fn(&p.stats)
}
