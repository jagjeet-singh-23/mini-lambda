package domain

import "time"

// PoolStats represents statistics about a container pool
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
	CreatedAt       time.Time
}
