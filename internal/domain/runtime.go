package domain

import (
	"context"
)

type Runtime interface {
	// Execute runs a function and returns the result
	// ctx allows for timeout and cancellation
	// function contains the code and configuration
	// input is the payload to pass to the function
	Execute(ctx context.Context, function *Function, input []byte) (*ExecutionResult, error)

	// Cleanup releases resources associated with the Runtime
	Cleanup() error
}

type ExecutionResult struct {
	// Output is the data returned by the function
	Output []byte

	// Logs captured during execution
	Logs []byte

	// MemoryUsed is the peak memory consumption in bytes
	MemoryUsed int64

	// Duration is how long the execution took
	Duration int64 // milliseconds

	// ExitCode is the process exit code (0 = success)
	ExitCode int
}

type ResourceLimits struct {
	MaxMemoryMB    int64
	MaxCPUPercent  int64
	TimeoutSeconds int64
	MaxProcesses   int64
}

func DefaultLimits() ResourceLimits {
	return ResourceLimits{
		MaxMemoryMB:    128,
		MaxCPUPercent:  100,
		TimeoutSeconds: 30,
		MaxProcesses:   256,
	}
}

type RuntimeManager interface {
	// GetRuntime() returns a runtime for teh specified type
	GetRuntime(runtime string) (Runtime, error)

	// ListRuntimes() returns all available runtime types
	ListRuntimes() []string

	// Shutdown gracefully shut downs all runtimes
	Shutdown(ctx context.Context) error
}

var (
	ErrRuntimeNotFound      = NewDomainError("runtime not found")
	ErrExecutionFailed      = NewDomainError("executioni failed")
	ErrResourceExceeded     = NewDomainError("resource limit exceeded")
	ErrExecutionStartFailed = NewDomainError("failed to start container")
)
