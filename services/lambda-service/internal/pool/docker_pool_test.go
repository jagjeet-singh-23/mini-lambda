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

	// Wait for TTL (100ms) + at least two ticks (50ms each) for eviction to register.
	time.Sleep(400 * time.Millisecond)

	stats := p.Stats()
	if stats.TotalEvictions < 1 {
		t.Errorf("TotalEvictions = %d after 400ms, want >= 1 (eviction did not happen)", stats.TotalEvictions)
	}

	// Replenishment is async (background goroutine creates a new container).
	// Block on Acquire with a generous timeout instead of a fixed sleep.
	acquireCtx, acquireCancel := context.WithTimeout(ctx, 30*time.Second)
	defer acquireCancel()

	c2, err := p.Acquire(acquireCtx)
	if err != nil {
		t.Fatalf("post-eviction Acquire timed out — replenishment did not complete: %v", err)
	}
	if c2.ID == firstID {
		t.Errorf("got same container ID after eviction: %s", c2.ID)
	}
	if s := p.Stats(); s.ColdStarts < 2 {
		t.Errorf("ColdStarts = %d, want >= 2 (initial fill + replenishment)", s.ColdStarts)
	}
	p.Release(ctx, c2)
}
