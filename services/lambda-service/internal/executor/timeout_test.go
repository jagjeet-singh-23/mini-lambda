package executor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/pool"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

// memStorage is an in-memory domain.CodeStorage for tests.
type memStorage struct {
	mu   sync.Mutex
	code []byte
}

func (s *memStorage) Store(_ context.Context, id string, _ []byte) (string, error) {
	return id, nil
}
func (s *memStorage) Retrieve(_ context.Context, _ string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code, nil
}
func (s *memStorage) Delete(_ context.Context, _ string) error  { return nil }
func (s *memStorage) Exists(_ context.Context, _ string) (bool, error) { return true, nil }

// TestExecute_TimeoutKillsContainer verifies that when a function's execution
// exceeds its timeout, the error wraps context.DeadlineExceeded and the
// container used for that execution is force-stopped and removed from Docker
// (not returned to the idle pool with a zombie exec still running inside it).
func TestExecute_TimeoutKillsContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}

	ctx := context.Background()

	// Handler that sleeps 2 seconds — used for both the warmup (Timeout=30s, succeeds)
	// and the timeout test (Timeout=500ms, times out after 500ms).
	storage := &memStorage{code: []byte(`
def handler(event, context):
    import time
    time.sleep(2)
    return {}
`)}

	// TickInterval=30s ensures the lifecycle goroutine does not interfere during the test.
	cfg := pool.PoolConfig{
		Runtime:      "python3.9",
		MinSize:      1,
		MaxSize:      2,
		MaxIdleTime:  5 * time.Minute,
		MaxUseCount:  100,
		TickInterval: 30 * time.Second,
	}

	rt, err := NewDockerRuntime("python3.9", "python:3.9-slim", nil, cfg, storage)
	if err != nil {
		t.Fatalf("NewDockerRuntime: %v", err)
	}
	defer rt.Cleanup()

	svcCtx, svcCancel := context.WithCancel(ctx)
	defer svcCancel()
	rt.Start(svcCtx)

	fn := &domain.Function{
		ID:      "timeout-test-fn",
		Name:    "timeout-test-fn",
		Runtime: "python3.9",
		Handler: "handler",
		Code:    []byte("timeout-test-key"),
		Memory:  128,
		Timeout: 30 * time.Second,
	}

	// Warmup: run the handler successfully so pool pre-warms container C1.
	// The sleep handler takes ~2s; Timeout=30s so it succeeds.
	_, err = rt.Execute(ctx, fn, []byte(`{}`))
	if err != nil {
		t.Fatalf("warmup execution failed: %v", err)
	}

	// C1 is now sitting in the idle pool. List managed containers to record C1's ID.
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	// Filter by both the managed label and the Python image to avoid picking up
	// containers from other packages running in parallel (e.g. pool tests use alpine).
	f := filters.NewArgs(
		filters.Arg("label", "mini-lambda.managed=true"),
		filters.Arg("ancestor", "python:3.9-slim"),
	)
	before, err := cli.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		t.Fatalf("ContainerList before: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("no managed Python containers found after warmup — pool did not pre-warm")
	}
	c1ID := before[0].ID

	// Timeout test: same function, short timeout.
	// Acquire pulls C1 (warm start), exec starts the 2s sleep, times out after 500ms.
	fn.Timeout = 500 * time.Millisecond
	_, err = rt.Execute(ctx, fn, []byte(`{}`))
	if err == nil {
		t.Fatal("Execute should have returned a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want error wrapping context.DeadlineExceeded, got: %v", err)
	}

	// Wait for the async discard goroutine to complete.
	// ContainerStop sends SIGTERM then waits up to 2s before SIGKILL, plus removal.
	time.Sleep(6 * time.Second)

	// C1 must be gone — it should have been force-stopped and removed,
	// not returned to the idle pool with the zombie sleep exec still running.
	after, err := cli.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		t.Fatalf("ContainerList after: %v", err)
	}
	for _, c := range after {
		if c.ID == c1ID {
			t.Errorf("timed-out container %s is still running — it was not force-stopped and removed", c1ID[:12])
		}
	}
}
