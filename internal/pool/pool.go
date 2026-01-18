package pool

import (
	"context"
	"time"
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
	// Acquire gets a warm container from the pool
	Acquire(ctx context.Context) (*Container, error)

	// Release returns a container to the pool
	Release(ctx context.Context, container *Container) error

	// CreateNew creates a new container and adds it to the pool
	CreateNew(ctx context.Context) (*Container, error)

	// Evict removes a container from the pool (LRU strategy)
	Evict(ctx context.Context) error

	// Size returns the number of containers in the pool
	Size() int

	// Stats returns the pool statistics
	Stats() PoolStats

	// Shutdown shuts down the pool
	Shutdown(ctx context.Context) error
}

type PoolStats struct {
	Runtime         string
	TotalContainers int
	WarmContainers  int
	InUseContainers int
	HitRate         float64
	ColdStarts      int64
	WarmStarts      int64
	TotalEvictions  int64
	AverageUseCount float64

	CreatedAt time.Time
}

type PoolConfig struct {
	Runtime         string
	MinSize         int
	MaxSize         int
	MaxIdleTime     time.Duration
	MaxUseCount     int64
	CleanupStrategy CleanupStrategy
}

type CleanupStrategy int

const (
	CleanupMinimal    CleanupStrategy = iota // reset working directory
	CleanupStandard                          // Reset filesystem changes, clear temp files
	CleanupAggressive                        // Kill all processes, reset everything
)

func DefaultPoolConfig(runtime string) PoolConfig {
	return PoolConfig{
		Runtime:         runtime,
		MinSize:         2,
		MaxSize:         10,
		MaxIdleTime:     5 * time.Minute,
		MaxUseCount:     100,
		CleanupStrategy: CleanupStandard,
	}
}

type PoolManager interface {
	GetPool(runtime string) (ContainerPool, error)
	WarmUp(ctx context.Context, runtimes []string) error
	Shutdown(ctx context.Context) error
	GlobalStats() map[string]PoolStats
}
