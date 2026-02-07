package executor

import (
	"fmt"
	"time"
)

type ExecutionMetrics struct {
	// Pool operations
	PoolAcquireTime time.Duration
	PoolReleaseTime time.Duration

	// Container exec operations
	ExecCreateTime time.Duration
	ExecAttachTime time.Duration
	ExecStartTime  time.Duration
	ExecWaitTime   time.Duration

	// I/O Operations
	OutputReadTime time.Duration
	LogCollectTime time.Duration

	// Actual Execution
	CodeExecutionTime time.Duration

	// Total
	TotalTime time.Duration

	// Flags
	WasWarmStart bool
	ContainerID  string
}

// String returns a formatted breakdown of metrics
func (m *ExecutionMetrics) String() string {
	return fmt.Sprintf(`
Execution Metrics (Container: %s)
═══════════════════════════════════════════════
Warm Start:        %v
Total Time:        %s

Breakdown:
──────────────────────────────────────────────
Pool Acquire:      %6s  (%5.1f%%)
Exec Create:       %6s  (%5.1f%%)
Exec Attach:       %6s  (%5.1f%%)
Exec Start:        %6s  (%5.1f%%)
Code Execution:    %6s  (%5.1f%%)
Exec Wait:         %6s  (%5.1f%%)
Output Read:       %6s  (%5.1f%%)
Pool Release:      %6s  (%5.1f%%)
═══════════════════════════════════════════════
`,
		m.ContainerID[:12],
		m.WasWarmStart,
		m.TotalTime,
		m.PoolAcquireTime, m.percentage(m.PoolAcquireTime),
		m.ExecCreateTime, m.percentage(m.ExecCreateTime),
		m.ExecAttachTime, m.percentage(m.ExecAttachTime),
		m.ExecStartTime, m.percentage(m.ExecStartTime),
		m.CodeExecutionTime, m.percentage(m.CodeExecutionTime),
		m.ExecWaitTime, m.percentage(m.ExecWaitTime),
		m.OutputReadTime, m.percentage(m.OutputReadTime),
		m.PoolReleaseTime, m.percentage(m.PoolReleaseTime),
	)
}

func (m *ExecutionMetrics) percentage(d time.Duration) float64 {
	if m.TotalTime == 0 {
		return 0
	}

	return float64(d) / float64(m.TotalTime) * 100
}

type Timer struct {
	start time.Time
}

func NewTimer() *Timer {
	return &Timer{
		start: time.Now(),
	}
}

func (t *Timer) Elapsed() time.Duration {
	return time.Since(t.start)
}
