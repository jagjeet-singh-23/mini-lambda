package domain

import (
	"time"
)

const oneGb = 1024

type Function struct {
	ID          string            // ID of the unique identifier
	Name        string            // Human-readable name
	Runtime     string            // execution environment
	Handler     string            // code entry-point
	Code        []byte            // function code as bytes
	Timeout     time.Duration     // max execution time allowed
	Memory      int64             // max memory allocated
	Environment map[string]string // env vars
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (f *Function) Validate() error {
	if f.Name == "" {
		return ErrInvalidFunctionName
	}

	if f.Runtime == "" {
		return ErrInvalidRuntime
	}

	if len(f.Code) == 0 {
		return ErrEmptyCode
	}

	if f.Memory <= 0 {
		return ErrInvalidMemory
	}

	if f.Timeout > 15*time.Minute {
		return ErrTimeoutTooLong
	}

	if f.Memory > 10*oneGb {
		return ErrMemoryTooHigh
	}

	return nil
}

var (
	ErrInvalidFunctionName = NewDomainError("function name cannot be empty")
	ErrInvalidRuntime      = NewDomainError("runtime must be specified")
	ErrEmptyCode           = NewDomainError("function code cannot be empty")
	ErrInvalidTimeout      = NewDomainError("timeout must be positive")
	ErrInvalidMemory       = NewDomainError("memory must be positive")
	ErrTimeoutTooLong      = NewDomainError("timeout cannot exceed 15 minutes")
	ErrMemoryTooHigh       = NewDomainError("memory cannot exceed 10GB")
)

type DomainError struct {
	Message string
}

func (e *DomainError) Error() string {
	return e.Message
}

func NewDomainError(msg string) *DomainError {
	return &DomainError{Message: msg}
}
