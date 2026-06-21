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
