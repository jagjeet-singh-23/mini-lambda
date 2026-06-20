package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

// stubRepo counts DB hits and returns a fixed function.
type stubRepo struct {
	hits atomic.Int64
	fn   *domain.Function
	err  error
}

func (s *stubRepo) FindByID(_ context.Context, id string) (*domain.Function, error) {
	s.hits.Add(1)
	// Simulate DB latency so concurrent callers have time to pile up.
	time.Sleep(20 * time.Millisecond)
	return s.fn, s.err
}
func (s *stubRepo) FindByName(_ context.Context, _ string) (*domain.Function, error) {
	return nil, nil
}
func (s *stubRepo) Save(_ context.Context, _ *domain.Function) error         { return nil }
func (s *stubRepo) List(_ context.Context, _, _ int) ([]*domain.Function, error) {
	return nil, nil
}
func (s *stubRepo) Delete(_ context.Context, _ string) error        { return nil }
func (s *stubRepo) Count(_ context.Context) (int64, error)          { return 0, nil }
func (s *stubRepo) Exists(_ context.Context, _ string) (bool, error) { return false, nil }

// noopCache is always a cache miss — forces every request through to the repo.
type noopCache struct{}

func (n *noopCache) GetFunction(_ context.Context, _ string) (*domain.Function, error) {
	return nil, nil
}
func (n *noopCache) SetFunction(_ context.Context, _ *domain.Function) error { return nil }
func (n *noopCache) SetFunctionWithTTL(_ context.Context, _ *domain.Function, _ time.Duration) error {
	return nil
}
func (n *noopCache) DeleteFunction(_ context.Context, _ string) error { return nil }

// TestFindByID_SingleflightCollapsesConcurrentCacheMisses verifies that when N
// goroutines concurrently call FindByID for the same key and the cache is cold,
// only one DB query is issued regardless of N.
func TestFindByID_SingleflightCollapsesConcurrentCacheMisses(t *testing.T) {
	repo := &stubRepo{fn: &domain.Function{
		ID: "fn-1", Name: "test", Runtime: "nodejs18",
		Memory: 128, Timeout: 30 * time.Second,
	}}

	cached := NewCachedFunctionRepository(repo, &noopCache{})

	const concurrency = 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			_, err := cached.FindByID(context.Background(), "fn-1")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if hits := repo.hits.Load(); hits != 1 {
		t.Errorf("expected exactly 1 DB query, got %d", hits)
	}
}

// TestFindByID_DifferentKeysQueryDBIndependently verifies that singleflight
// does not incorrectly collapse requests for different function IDs.
func TestFindByID_DifferentKeysQueryDBIndependently(t *testing.T) {
	makeRepo := func(id string) *stubRepo {
		return &stubRepo{fn: &domain.Function{
			ID: id, Name: id, Runtime: "nodejs18",
			Memory: 128, Timeout: 30 * time.Second,
		}}
	}

	repo1, repo2 := makeRepo("fn-a"), makeRepo("fn-b")

	// We need a single repo that handles both IDs; combine into one.
	combined := &dualStubRepo{a: repo1, b: repo2}
	cached := NewCachedFunctionRepository(combined, &noopCache{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cached.FindByID(context.Background(), "fn-a") }()
	go func() { defer wg.Done(); cached.FindByID(context.Background(), "fn-b") }()
	wg.Wait()

	if repo1.hits.Load() != 1 {
		t.Errorf("fn-a: expected 1 DB hit, got %d", repo1.hits.Load())
	}
	if repo2.hits.Load() != 1 {
		t.Errorf("fn-b: expected 1 DB hit, got %d", repo2.hits.Load())
	}
}

// TestSetFunction_TTLJitterSpreadsCacheExpiry verifies that N calls to SetFunction
// for different keys do not all use the exact same TTL, which would cause
// synchronised expiry (cache stampede).
func TestSetFunction_TTLJitterSpreadsCacheExpiry(t *testing.T) {
	spy := &ttlSpyCache{}
	cached := NewCachedFunctionRepository(&stubRepo{}, spy)

	const n = 20
	for i := 0; i < n; i++ {
		fn := &domain.Function{
			ID: fmt.Sprintf("fn-%d", i), Name: "test", Runtime: "nodejs18",
			Memory: 128, Timeout: 30 * time.Second,
		}
		_ = cached.setWithJitter(context.Background(), fn)
	}

	if len(spy.ttls) == 0 {
		t.Fatal("no TTLs recorded")
	}
	first := spy.ttls[0]
	allSame := true
	for _, ttl := range spy.ttls[1:] {
		if ttl != first {
			allSame = false
			break
		}
	}
	if allSame {
		t.Errorf("all %d TTLs are identical (%v): jitter is not applied", n, first)
	}
}

type ttlSpyCache struct {
	ttls []time.Duration
	mu   sync.Mutex
}

func (s *ttlSpyCache) GetFunction(_ context.Context, _ string) (*domain.Function, error) {
	return nil, nil
}
func (s *ttlSpyCache) SetFunction(_ context.Context, _ *domain.Function) error { return nil }
func (s *ttlSpyCache) SetFunctionWithTTL(_ context.Context, _ *domain.Function, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ttls = append(s.ttls, ttl)
	return nil
}
func (s *ttlSpyCache) DeleteFunction(_ context.Context, _ string) error { return nil }

type dualStubRepo struct {
	a, b *stubRepo
}

func (d *dualStubRepo) FindByID(_ context.Context, id string) (*domain.Function, error) {
	switch id {
	case "fn-a":
		return d.a.FindByID(context.Background(), id)
	case "fn-b":
		return d.b.FindByID(context.Background(), id)
	}
	return nil, domain.ErrFunctionNotFound
}
func (d *dualStubRepo) FindByName(_ context.Context, _ string) (*domain.Function, error) {
	return nil, nil
}
func (d *dualStubRepo) Save(_ context.Context, _ *domain.Function) error         { return nil }
func (d *dualStubRepo) List(_ context.Context, _, _ int) ([]*domain.Function, error) {
	return nil, nil
}
func (d *dualStubRepo) Delete(_ context.Context, _ string) error        { return nil }
func (d *dualStubRepo) Count(_ context.Context) (int64, error)          { return 0, nil }
func (d *dualStubRepo) Exists(_ context.Context, _ string) (bool, error) { return false, nil }
