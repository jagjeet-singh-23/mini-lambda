package domain

import (
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/pkg/utils"
)

// ExecutionStatus represents the state of the function execution
type ExecutionStatus string

const (
	StatusPending ExecutionStatus = "pending" // queued but not started yet
	StatusRunning ExecutionStatus = "running" // currently in progress
	StatusSuccess ExecutionStatus = "success" // completed successfully
	StatusFailed  ExecutionStatus = "failed"  // failed with an error
	StatusTimeout ExecutionStatus = "timeout" // failed due to timeout
)

type Execution struct {
	// ID uniquely identifies this execution
	ID string

	// FunctionID links to the ID of the function being executed
	FunctionID string

	// Status tracks the current state
	Status ExecutionStatus

	// Input is the payload provided to the function
	Input []byte

	// Output is the result returned by the function
	Output []byte

	// Error contains the error message if execution failed
	Error string

	// StartedAt marks when execution started
	StartedAt time.Time

	// CompletedAt marks when the execution failed
	CompletedAt time.Time

	// Duration is the total execution time
	Duration time.Duration

	// MemoryUsed tracks peak memory consumption in bytes
	MemoryUsed int64
}

func (e *Execution) MarkStarted() {
	e.Status = StatusRunning
	e.StartedAt = time.Now()
}

func (e *Execution) MarkSuccess(output []byte) {
	e.Status = StatusSuccess
	e.CompletedAt = time.Now()
	e.Duration = e.CompletedAt.Sub(e.StartedAt)
	e.Output = output
}

func (e *Execution) MarkFailed(err error) {
	e.Status = StatusFailed
	e.Error = err.Error()
	e.CompletedAt = time.Now()
	e.Duration = e.CompletedAt.Sub(e.StartedAt)
}

func (e *Execution) MarkTimeout() {
	e.Status = StatusTimeout
	e.Error = "execution exceeded timeout limit"
	e.CompletedAt = time.Now()
	e.Duration = e.CompletedAt.Sub(e.StartedAt)
}

func (e *Execution) IsComplete() bool {
	return e.Status == StatusSuccess ||
		e.Status == StatusFailed ||
		e.Status == StatusTimeout
}

// NewExecution creates a new execution instance
func NewExecution(functionID string, input []byte) *Execution {
	return &Execution{
		ID:         utils.GenerateID(), // TODO
		FunctionID: functionID,
		Status:     StatusPending,
		Input:      input,
		StartedAt:  time.Time{},
	}
}
