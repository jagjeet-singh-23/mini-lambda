# Container Pool Warm Starts + Graceful Shutdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire a channel-based container pool into the lambda invoke path so warm containers are reused across invocations, with TTL eviction, proactive MinSize replenishment, and WaitGroup-guarded goroutine shutdown.

**Architecture:** Each `DockerRuntime` owns a `DockerPool` keyed by runtime (e.g., `nodejs18`). The pool holds a buffered `idle chan *Container`; `Acquire` blocks until a container is available or the context expires. A background ticker goroutine evicts idle containers past their TTL and refills to `MinSize`. `executor.Manager.Start(ctx)` propagates the service lifetime context down to each pool.

**Tech Stack:** Go stdlib (`sync`, `context`, `time`), Docker SDK (`github.com/docker/docker`), standard `testing` package (no testify — not in go.mod).

## Global Constraints

- No new external dependencies — standard library + existing go.mod only
- All pool tests require a running Docker daemon; guard with `if testing.Short() { t.Skip(...) }`
- Use `alpine` image for pool tests (smallest, has `tail`)
- Do not touch `shared/domain/runtime.go` — use a local `startable` interface in `executor/manager.go` for type assertion
- `KubernetesPodPool` gets a no-op `Start` stub — do not change its existing logic
- Default pool sizes: MinSize=1, MaxSize=5, MaxIdleTime=5m, MaxUseCount=500, TickInterval=30s

---

## File Map

| File | Change |
|---|---|
| `services/lambda-service/internal/pool/pool.go` | Add `TickInterval` to `PoolConfig`, fix defaults, add `Start(ctx)` to `ContainerPool` interface |
| `services/lambda-service/internal/pool/docker_pool.go` | Full rewrite: channel-based `Acquire`, `Start`/`runLifecycle`/`evictAndReplenish`/`enqueueNew` |
| `services/lambda-service/internal/pool/kubernetes_pool.go` | Add no-op `Start` stub |
| `services/lambda-service/internal/pool/docker_pool_test.go` | New: 3 integration tests (warm reuse, blocking at MaxSize, TTL eviction+replenishment) |
| `services/lambda-service/internal/executor/docker.go` | Accept `poolCfg pool.PoolConfig`, simplify `acquireContainer`, add `Start(ctx)`, fix `Cleanup` |
| `services/lambda-service/internal/executor/manager.go` | Accept `poolCfg`, add `startable` interface, add `Manager.Start(ctx)` |
| `services/lambda-service/cmd/main.go` | Read `POOL_*` env vars, build `pool.PoolConfig`, call `runtimeManager.Start(ctx)`, add `sync.WaitGroup` |

---

### Task 1: Update `pool/pool.go` and add stubs

**Files:**
- Modify: `services/lambda-service/internal/pool/pool.go`
- Modify: `services/lambda-service/internal/pool/kubernetes_pool.go`
- Modify: `services/lambda-service/internal/pool/docker_pool.go` (stub only)

**Interfaces:**
- Produces: `ContainerPool.Start(ctx context.Context)` on the interface; `PoolConfig.TickInterval time.Duration`; updated `DefaultPoolConfig` defaults

- [ ] **Step 1: Update `pool/pool.go`**

Replace the entire file with:

```go
package pool

import (
	"context"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

type Container struct {
	ID         string
	Runtime    string
	CreatedAt  time.Time
	LastUsed   time.Time
	UseCount   int64
	State      ContainerState
	MemoryUsed int64
}

type ContainerState int

const (
	StateWarm ContainerState = iota
	StateInUse
	StateCleaning
	StateEvicted
)

func (s ContainerState) String() string {
	switch s {
	case StateWarm:
		return "warm"
	case StateInUse:
		return "in-use"
	case StateCleaning:
		return "cleaning"
	case StateEvicted:
		return "evicted"
	default:
		return "unknown"
	}
}

type ContainerPool interface {
	// Start pre-fills the pool to MinSize and launches the lifecycle goroutine.
	Start(ctx context.Context)

	// Acquire blocks until a warm container is available or ctx is cancelled.
	Acquire(ctx context.Context) (*Container, error)

	// Release returns a container to the pool, or retires it if MaxUseCount is reached.
	Release(ctx context.Context, container *Container) error

	// CreateNew creates a new container and adds it to the idle pool.
	// Returns nil, nil on success (container enters idle channel).
	CreateNew(ctx context.Context) (*Container, error)

	// Evict removes one warm container from the pool.
	Evict(ctx context.Context) error

	// Size returns total containers (idle + in-use).
	Size() int

	// Stats returns pool statistics.
	Stats() domain.PoolStats

	// Shutdown stops and removes all containers.
	Shutdown(ctx context.Context) error
}

type PoolConfig struct {
	Runtime      string
	MinSize      int
	MaxSize      int
	MaxIdleTime  time.Duration
	MaxUseCount  int64
	TickInterval time.Duration
}

func DefaultPoolConfig(runtime string) PoolConfig {
	return PoolConfig{
		Runtime:      runtime,
		MinSize:      1,
		MaxSize:      5,
		MaxIdleTime:  5 * time.Minute,
		MaxUseCount:  500,
		TickInterval: 30 * time.Second,
	}
}

type PoolManager interface {
	GetPool(runtime string) (ContainerPool, error)
	WarmUp(ctx context.Context, runtimes []string) error
	Shutdown(ctx context.Context) error
	GlobalStats() map[string]domain.PoolStats
}
```

- [ ] **Step 2: Add no-op `Start` to `KubernetesPodPool`**

Add this method anywhere in `services/lambda-service/internal/pool/kubernetes_pool.go`:

```go
func (p *KubernetesPodPool) Start(_ context.Context) {}
```

- [ ] **Step 3: Add stub `Start` to `DockerPool`** (will be replaced in Task 2)

Add this temporary method anywhere in `services/lambda-service/internal/pool/docker_pool.go`:

```go
func (p *DockerPool) Start(_ context.Context) {}
```

- [ ] **Step 4: Verify it compiles**

```bash
cd services/lambda-service && go build ./internal/pool/...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add services/lambda-service/internal/pool/pool.go \
        services/lambda-service/internal/pool/kubernetes_pool.go \
        services/lambda-service/internal/pool/docker_pool.go
git commit -m "feat: add TickInterval to PoolConfig and Start to ContainerPool interface"
```

---

### Task 2: Rewrite `DockerPool` + write integration tests (TDD)

**Files:**
- Create: `services/lambda-service/internal/pool/docker_pool_test.go`
- Modify: `services/lambda-service/internal/pool/docker_pool.go` (full rewrite)

**Interfaces:**
- Consumes: `PoolConfig.TickInterval`, `ContainerPool.Start(ctx)` from Task 1
- Produces: fully functional `DockerPool` with channel-based `Acquire` that blocks; `Start`/lifecycle goroutine; TTL eviction + replenishment

- [ ] **Step 1: Write the three failing tests**

Create `services/lambda-service/internal/pool/docker_pool_test.go`:

```go
package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
)

func testCfg(minSize, maxSize int, idleTTL, tick time.Duration) pool.PoolConfig {
	return pool.PoolConfig{
		Runtime:      "test",
		MinSize:      minSize,
		MaxSize:      maxSize,
		MaxIdleTime:  idleTTL,
		MaxUseCount:  100,
		TickInterval: tick,
	}
}

// TestDockerPool_WarmReuse verifies that releasing a container and acquiring again
// returns the same container (no extra container is created).
func TestDockerPool_WarmReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	p, err := pool.NewDockerPool(testCfg(0, 1, 5*time.Minute, 30*time.Second), "alpine")
	if err != nil {
		t.Fatalf("NewDockerPool: %v", err)
	}
	defer p.Shutdown(context.Background())

	if _, err := p.CreateNew(ctx); err != nil {
		t.Fatalf("CreateNew: %v", err)
	}

	c1, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstID := c1.ID
	firstUse := c1.UseCount

	if err := p.Release(ctx, c1); err != nil {
		t.Fatalf("Release: %v", err)
	}

	c2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if c2.ID != firstID {
		t.Errorf("got container %s, want %s (warm reuse failed)", c2.ID, firstID)
	}
	if c2.UseCount <= firstUse {
		t.Errorf("UseCount did not increment: got %d, want > %d", c2.UseCount, firstUse)
	}
	if got := p.Size(); got != 1 {
		t.Errorf("pool size = %d, want 1", got)
	}
	p.Release(ctx, c2)
}

// TestDockerPool_BlocksAtMaxSize verifies that Acquire blocks when all MaxSize
// containers are in-use, and unblocks exactly when one is released.
func TestDockerPool_BlocksAtMaxSize(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	p, err := pool.NewDockerPool(testCfg(0, 2, 5*time.Minute, 30*time.Second), "alpine")
	if err != nil {
		t.Fatalf("NewDockerPool: %v", err)
	}
	defer p.Shutdown(context.Background())

	if _, err := p.CreateNew(ctx); err != nil {
		t.Fatalf("CreateNew 1: %v", err)
	}
	if _, err := p.CreateNew(ctx); err != nil {
		t.Fatalf("CreateNew 2: %v", err)
	}

	c1, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	c2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}

	// Third acquire must block — detect by timing out a short context.
	blockCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(blockCtx); err == nil {
		t.Fatal("third Acquire should have blocked but returned immediately")
	}

	// Release c1 → blocked acquire (from a goroutine) should unblock.
	got := make(chan *pool.Container, 1)
	go func() {
		c, err := p.Acquire(ctx)
		if err == nil {
			got <- c
		}
	}()

	time.Sleep(20 * time.Millisecond) // let goroutine reach the select
	if err := p.Release(ctx, c1); err != nil {
		t.Fatalf("Release c1: %v", err)
	}

	select {
	case c3 := <-got:
		if c3.ID != c1.ID {
			t.Errorf("unblocked acquire got %s, want %s", c3.ID, c1.ID)
		}
		p.Release(ctx, c3)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("goroutine did not unblock after Release")
	}
	p.Release(ctx, c2)
}

// TestDockerPool_TTLEvictionAndReplenishment verifies that idle containers are
// evicted after MaxIdleTime and the pool immediately refills to MinSize.
func TestDockerPool_TTLEvictionAndReplenishment(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, err := pool.NewDockerPool(
		testCfg(1, 2, 100*time.Millisecond, 50*time.Millisecond),
		"alpine",
	)
	if err != nil {
		t.Fatalf("NewDockerPool: %v", err)
	}
	defer p.Shutdown(context.Background())

	p.Start(ctx) // pre-fills to MinSize=1 and launches lifecycle goroutine

	// Grab the initial container ID.
	c, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("initial Acquire: %v", err)
	}
	firstID := c.ID
	p.Release(ctx, c)

	// Wait for TTL (100ms) + at least one tick (50ms) + buffer.
	time.Sleep(400 * time.Millisecond)

	// Pool must still hold MinSize=1.
	if got := p.Size(); got != 1 {
		t.Errorf("pool size = %d after eviction, want 1 (replenishment failed)", got)
	}

	stats := p.Stats()
	if stats.TotalEvictions < 1 {
		t.Errorf("TotalEvictions = %d, want >= 1", stats.TotalEvictions)
	}
	if stats.ColdStarts < 2 {
		t.Errorf("ColdStarts = %d, want >= 2 (initial fill + replenishment)", stats.ColdStarts)
	}

	// The replacement container must have a different ID.
	c2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("post-eviction Acquire: %v", err)
	}
	if c2.ID == firstID {
		t.Errorf("got same container ID after eviction: %s", c2.ID)
	}
	p.Release(ctx, c2)
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd services/lambda-service && go test ./internal/pool/... -v -run TestDockerPool_ -count=1
```

Expected: compilation error or runtime failure — `Acquire` currently returns nil (not blocking), `Start` is a stub.

- [ ] **Step 3: Rewrite `docker_pool.go`**

Replace the entire file with:

```go
package pool

import (
	"context"
	"fmt"
	"sync"
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
	go p.runLifecycle(ctx)
}

func (p *DockerPool) runLifecycle(ctx context.Context) {
	ticker := time.NewTicker(p.config.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
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

// Shutdown stops and removes all containers (idle and in-use).
func (p *DockerPool) Shutdown(ctx context.Context) error {
	close(p.idle)
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
func (p *DockerPool) evictAndReplenish(ctx context.Context) {
	var keep []*Container
	for {
		select {
		case c := <-p.idle:
			if time.Since(c.LastUsed) < p.config.MaxIdleTime {
				keep = append(keep, c)
			} else {
				p.stopDockerContainer(ctx, c.ID)
				p.updateStats(func(m *poolMetrics) { m.totalEvictions++ })
			}
		default:
			goto done
		}
	}
done:
	for _, c := range keep {
		select {
		case p.idle <- c:
		default:
			p.stopDockerContainer(ctx, c.ID)
		}
	}
	for len(p.idle) < p.config.MinSize {
		if err := p.enqueueNew(ctx); err != nil {
			break
		}
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
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
cd services/lambda-service && go test ./internal/pool/... -v -run TestDockerPool_ -count=1
```

Expected: all three tests PASS. This will pull the `alpine` image on first run (~10s). Subsequent runs are fast.

- [ ] **Step 5: Verify the whole package still compiles**

```bash
cd services/lambda-service && go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add services/lambda-service/internal/pool/docker_pool.go \
        services/lambda-service/internal/pool/docker_pool_test.go
git commit -m "feat: rewrite DockerPool with channel-based Acquire and lifecycle goroutine"
```

---

### Task 3: Wire `Start` and pool config through the executor

**Files:**
- Modify: `services/lambda-service/internal/executor/docker.go`
- Modify: `services/lambda-service/internal/executor/manager.go`

**Interfaces:**
- Consumes: `pool.ContainerPool.Start(ctx)`, `pool.ContainerPool.Shutdown(ctx)`, `pool.PoolConfig` from Tasks 1–2
- Produces: `executor.Manager.Start(ctx context.Context)` for main.go to call

- [ ] **Step 1: Update `executor/docker.go`**

Make three targeted changes:

**1a. Update `NewDockerRuntime` to accept `poolCfg`** — replace the signature and `initContainerPool` call:

```go
// NewDockerRuntime creates a new Docker-based runtime
func NewDockerRuntime(
	runtimeType, baseImage string,
	metricsCollector *metrics.MetricsCollector,
	poolCfg pool.PoolConfig,
) (*DockerRuntime, error) {
	if runtimeType == "" || baseImage == "" {
		return nil, fmt.Errorf("runtime type and base image cannot be empty")
	}

	cli, err := initDockerClient()
	if err != nil {
		return nil, err
	}

	containerPool, err := initContainerPool(poolCfg, runtimeType, baseImage)
	if err != nil {
		return nil, err
	}

	return &DockerRuntime{
		runtimeType:      runtimeType,
		baseImage:        baseImage,
		client:           cli,
		Pool:             containerPool,
		metricsCollector: metricsCollector,
	}, nil
}
```

**1b. Update `initContainerPool` to use the passed config:**

```go
func initContainerPool(cfg pool.PoolConfig, runtimeType, baseImage string) (pool.ContainerPool, error) {
	cfg.Runtime = runtimeType
	containerPool, err := pool.NewDockerPool(cfg, baseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to create container pool: %w", err)
	}
	return containerPool, nil
}
```

**1c. Replace `acquireContainer` — remove `CreateNew` fallback, detect warm by UseCount:**

```go
func (r *DockerRuntime) acquireContainer(
	ctx context.Context,
	m *ExecutionMetrics,
) (*pool.Container, bool, error) {
	poolTimer := NewTimer()
	c, err := r.Pool.Acquire(ctx)
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
		fmt.Printf("❄️  COLD: Container %s (pool size: %d)\n", c.ID[:12], r.Pool.Size())
		if r.metricsCollector != nil {
			r.metricsCollector.RecordColdStart(r.runtimeType)
		}
	}
	return c, wasWarmStart, nil
}
```

**1d. Add `Start` method and fix `Cleanup`:**

```go
func (r *DockerRuntime) Start(ctx context.Context) {
	r.Pool.Start(ctx)
}

// Cleanup implements the Runtime interface.
func (r *DockerRuntime) Cleanup() error {
	if r.Pool != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.Pool.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}
```

- [ ] **Step 2: Update `executor/manager.go`**

**2a. Add `poolCfg` to `Manager` struct and `NewManager` signature:**

```go
type Manager struct {
	runtimes         map[string]domain.Runtime
	mu               sync.RWMutex
	metricsCollector *metrics.MetricsCollector
	s3Storage        *storage.S3Storage
	poolCfg          pool.PoolConfig
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
```

**2b. Thread `poolCfg` to `NewDockerRuntime` inside `registerDefaultRuntimes`:**

```go
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
```

**2c. Add `startable` interface and `Manager.Start`** (add to end of file):

```go
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
```

**2d. Add the pool import** to the import block in `manager.go`:

```go
"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
```

- [ ] **Step 3: Verify it compiles**

```bash
cd services/lambda-service && go build ./internal/executor/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add services/lambda-service/internal/executor/docker.go \
        services/lambda-service/internal/executor/manager.go
git commit -m "feat: thread pool config through executor and add Start/Cleanup to DockerRuntime"
```

---

### Task 4: `main.go` — env-var config, `Start` call, and `WaitGroup` shutdown

**Files:**
- Modify: `services/lambda-service/cmd/main.go`

**Interfaces:**
- Consumes: `executor.Manager.Start(ctx)` from Task 3; `pool.PoolConfig` from Task 1

- [ ] **Step 1: Add pool-related fields to `Config` and update `loadConfig`**

Add to the `Config` struct:

```go
type Config struct {
	PostgresHost string
	PostgresPort string
	PostgresUser string
	PostgresPass string
	PostgresDB   string
	PostgresSSL  string
	S3Endpoint   string
	S3Region     string
	S3AccessKey  string
	S3SecretKey  string
	S3Bucket     string
	RabbitMQURL  string
	RedisAddr    string
	PoolMinSize  int
	PoolMaxSize  int
	PoolIdleTTL  time.Duration
}
```

Update `loadConfig`:

```go
func loadConfig() Config {
	return Config{
		PostgresHost: getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort: getEnv("POSTGRES_PORT", "5432"),
		PostgresUser: getEnv("POSTGRES_USER", "postgres"),
		PostgresPass: getEnv("POSTGRES_PASSWORD", "postgres"),
		PostgresDB:   getEnv("POSTGRES_DB", "lambda_service_db"),
		PostgresSSL:  getEnv("POSTGRES_SSLMODE", "disable"),
		S3Endpoint:   getEnv("S3_ENDPOINT", ""),
		S3Region:     getEnv("S3_REGION", "ap-south-1"),
		S3AccessKey:  getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:  getEnv("S3_SECRET_KEY", ""),
		S3Bucket:     getEnv("S3_BUCKET", ""),
		RabbitMQURL:  getEnv("RABBITMQ_URL", ""),
		RedisAddr:    getEnv("REDIS_CACHE_ADDR", ""),
		PoolMinSize:  getEnvInt("POOL_MIN_SIZE", 1),
		PoolMaxSize:  getEnvInt("POOL_MAX_SIZE", 5),
		PoolIdleTTL:  getEnvDuration("POOL_IDLE_TTL", 5*time.Minute),
	}
}
```

Add helper functions at the bottom of main.go (alongside the existing `getEnv`):

```go
func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
```

- [ ] **Step 2: Update imports in `main.go`**

Add to the import block:

```go
"strconv"
"sync"

"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
```

- [ ] **Step 3: Build `poolCfg` and update `NewManager` call in `main()`**

Replace:

```go
runtimeManager, err := executor.NewManager(s3Storage)
```

With:

```go
poolCfg := pool.PoolConfig{
    MinSize:      config.PoolMinSize,
    MaxSize:      config.PoolMaxSize,
    MaxIdleTime:  config.PoolIdleTTL,
    MaxUseCount:  500,
    TickInterval: 30 * time.Second,
}

runtimeManager, err := executor.NewManager(s3Storage, poolCfg)
```

- [ ] **Step 4: Call `runtimeManager.Start(ctx)` before the HTTP server**

In `main()`, after `cronScheduler` is set up and before the `go func() { server.ListenAndServe() }` goroutine, add:

```go
runtimeManager.Start(ctx)
```

- [ ] **Step 5: Add `sync.WaitGroup` and wrap background goroutines**

Replace the three `go func()` goroutines (registration consumer, event bus, cron) with WaitGroup-tracked versions:

```go
var wg sync.WaitGroup

wg.Add(1)
go func() {
    defer wg.Done()
    if err := registrationConsumer.Start(ctx); err != nil {
        log.Printf("Registration consumer error: %v", err)
    }
}()

wg.Add(1)
go func() {
    defer wg.Done()
    if err := eventBus.Start(ctx); err != nil {
        log.Printf("Event bus error: %v", err)
    }
}()

wg.Add(1)
go func() {
    defer wg.Done()
    if err := cronScheduler.Start(ctx); err != nil {
        log.Printf("Cron scheduler error: %v", err)
    }
}()

// HTTP server goroutine — not in WaitGroup; server.Shutdown handles it.
go func() {
    log.Printf("✅ Lambda Service listening on %s", server.Addr)
    if err := server.ListenAndServe(); err != http.ErrServerClosed {
        log.Fatalf("Server error: %v", err)
    }
}()

waitForShutdown(server, cancel, &wg)
```

- [ ] **Step 6: Update `waitForShutdown` to accept and wait on the WaitGroup**

```go
func waitForShutdown(server *http.Server, cancel context.CancelFunc, wg *sync.WaitGroup) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("🛑 Shutdown signal received, gracefully stopping...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	wg.Wait()
	log.Println("✅ All goroutines stopped")
}
```

- [ ] **Step 7: Verify the service compiles**

```bash
cd services/lambda-service && go build ./...
```

Expected: no errors.

- [ ] **Step 8: Run all tests**

```bash
cd services/lambda-service && go test ./... -short -v
```

Expected: existing tests pass; pool integration tests skipped (need Docker, skipped by `-short`).

- [ ] **Step 9: Commit**

```bash
git add services/lambda-service/cmd/main.go
git commit -m "feat: wire pool env-var config, Start call, and WaitGroup graceful shutdown"
```
