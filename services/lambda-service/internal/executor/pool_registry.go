package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

type functionEntry struct {
	codeKey string
	p       *pool.DockerPool
}

// PoolRegistry manages one DockerPool per function ID.
// Pools are created lazily on first invocation and replaced when a function's
// code key changes (i.e. when the function is updated).
type PoolRegistry struct {
	mu         sync.RWMutex
	entries    map[string]*functionEntry // functionID → entry
	poolCfg    pool.PoolConfig
	storage    domain.CodeStorage
	docker     *dockerclient.Client
	serviceCtx context.Context // set by Start(); propagated to pool lifecycle goroutines
}

func NewPoolRegistry(cfg pool.PoolConfig, storage domain.CodeStorage, docker *dockerclient.Client) *PoolRegistry {
	return &PoolRegistry{
		entries: make(map[string]*functionEntry),
		poolCfg: cfg,
		storage: storage,
		docker:  docker,
	}
}

// Start stores the service context so that lazily-created pools bind their
// lifecycle goroutines to the service lifetime.
func (r *PoolRegistry) Start(ctx context.Context) {
	r.mu.Lock()
	r.serviceCtx = ctx
	r.mu.Unlock()
}

// Acquire returns a warm container for fn, and the pool it came from.
// Pass the returned pool to Release — do not re-resolve from the registry.
// fn.Code must contain the S3 key (not code bytes) — use GetFunctionMeta, not GetFunction.
func (r *PoolRegistry) Acquire(ctx context.Context, fn *domain.Function) (*pool.Container, *pool.DockerPool, error) {
	p, err := r.getOrCreate(fn)
	if err != nil {
		return nil, nil, err
	}
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	return c, p, nil
}

// Release returns c to the specific pool it was acquired from.
func (r *PoolRegistry) Release(ctx context.Context, c *pool.Container, p *pool.DockerPool) error {
	return p.Release(ctx, c)
}

// Discard force-stops and removes a container that cannot be safely returned to
// the pool (e.g. a timed-out exec left a zombie process inside it).
func (r *PoolRegistry) Discard(c *pool.Container, p *pool.DockerPool) {
	p.Discard(c)
}

// PoolStats returns pool statistics for a specific function.
func (r *PoolRegistry) PoolStats(functionID string) (domain.PoolStats, bool) {
	r.mu.RLock()
	entry, ok := r.entries[functionID]
	r.mu.RUnlock()
	if !ok {
		return domain.PoolStats{}, false
	}
	return entry.p.Stats(), true
}

// Shutdown stops and drains all per-function pools.
func (r *PoolRegistry) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	entries := make([]*functionEntry, 0, len(r.entries))
	for _, e := range r.entries {
		entries = append(entries, e)
	}
	r.entries = make(map[string]*functionEntry)
	r.mu.Unlock()

	var errs []error
	for _, e := range entries {
		if err := e.p.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("pool registry shutdown errors: %v", errs)
	}
	return nil
}

// getOrCreate returns the pool for fn, creating it if it doesn't exist or if
// the function's code key has changed since the pool was created.
func (r *PoolRegistry) getOrCreate(fn *domain.Function) (*pool.DockerPool, error) {
	codeKey := string(fn.Code)

	r.mu.RLock()
	if entry, ok := r.entries[fn.ID]; ok && entry.codeKey == codeKey {
		p := entry.p
		r.mu.RUnlock()
		return p, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	// Re-check under write lock (double-checked locking).
	if entry, ok := r.entries[fn.ID]; ok && entry.codeKey == codeKey {
		r.mu.Unlock()
		return entry.p, nil
	}
	// Stale pool exists (function code was updated) — shut it down asynchronously.
	if old, ok := r.entries[fn.ID]; ok {
		go old.p.Shutdown(context.Background())
	}

	cfg := r.poolCfg
	cfg.Runtime = fn.Runtime
	cfg.SeedFunc = r.makeSeedFunc(fn.Runtime, codeKey)

	p, err := pool.NewDockerPool(cfg, baseImageForRuntime(fn.Runtime))
	if err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("create pool for function %s: %w", fn.ID, err)
	}

	svcCtx := r.serviceCtx
	if svcCtx == nil {
		svcCtx = context.Background()
	}
	// Insert entry before releasing lock so concurrent callers for the same
	// function find it immediately. Start() is called after unlocking to avoid
	// holding the lock across the expensive MinSize container fill.
	r.entries[fn.ID] = &functionEntry{codeKey: codeKey, p: p}
	r.mu.Unlock() // ← release before the blocking Start
	p.Start(svcCtx)
	return p, nil
}

// makeSeedFunc returns a SeedFunc closure that downloads code from S3 and
// writes it to /tmp/<handler file> in the container via docker cp.
func (r *PoolRegistry) makeSeedFunc(runtime, codeKey string) func(ctx context.Context, containerID string) (string, error) {
	return func(ctx context.Context, containerID string) (string, error) {
		code, err := r.storage.Retrieve(ctx, codeKey)
		if err != nil {
			return "", fmt.Errorf("retrieve code %s from S3: %w", codeKey, err)
		}
		if err := copyCodeToContainer(ctx, r.docker, containerID, code, runtime); err != nil {
			return "", fmt.Errorf("copy code to container: %w", err)
		}
		return codeKey, nil
	}
}

// copyCodeToContainer writes code bytes to /tmp/<filename> inside the container
// by piping through a `cat` exec. CopyToContainer is not used because /tmp is a
// tmpfs mount: docker cp writes to the overlay layer, which the tmpfs then hides.
// Piping via exec writes directly to the running tmpfs.
func copyCodeToContainer(ctx context.Context, docker *dockerclient.Client, containerID string, code []byte, runtime string) error {
	filename := handlerFilename(runtime)

	exec, err := docker.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          []string{"sh", "-c", "cat > /tmp/" + filename},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("exec create for seeding: %w", err)
	}
	resp, err := docker.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("exec attach for seeding: %w", err)
	}
	defer resp.Close()

	if err := docker.ContainerExecStart(ctx, exec.ID, container.ExecStartOptions{}); err != nil {
		return fmt.Errorf("exec start for seeding: %w", err)
	}
	if _, err := resp.Conn.Write(code); err != nil {
		return fmt.Errorf("write handler code: %w", err)
	}
	if err := resp.CloseWrite(); err != nil {
		return fmt.Errorf("close write for seeding: %w", err)
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := docker.ContainerExecInspect(ctx, exec.ID)
			if err != nil {
				return fmt.Errorf("inspect seeding exec: %w", err)
			}
			if !info.Running {
				if info.ExitCode != 0 {
					return fmt.Errorf("seeding exec failed (exit %d)", info.ExitCode)
				}
				return nil
			}
		}
	}
}

func handlerFilename(runtime string) string {
	switch {
	case strings.HasPrefix(runtime, "python"):
		return "handler.py"
	case strings.HasPrefix(runtime, "nodejs"):
		return "handler.js"
	case strings.HasPrefix(runtime, "go"):
		return "handler.go"
	default:
		return "handler.sh"
	}
}

func baseImageForRuntime(runtime string) string {
	switch runtime {
	case "python3.9":
		return "python:3.9-slim"
	case "python3.11":
		return "python:3.11-slim"
	case "nodejs18":
		return "node:18-slim"
	case "nodejs20":
		return "node:20-slim"
	case "go1.21":
		return "golang:1.21-alpine"
	default:
		return "alpine"
	}
}
