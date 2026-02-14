package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"time"
)

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF-OPEN"
	default:
		return "UNKNOWN"
	}
}

type CircuitBreaker struct {
	maxFailures     int
	resetTimeout    time.Duration
	mu              sync.RWMutex
	state           State
	failures        int
	lastFailureTime time.Time
}

func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        StateClosed,
	}
}

// Call executes the given function using a strategy based on the current circuit state.
func (cb *CircuitBreaker) Call(ctx context.Context, fn func() error) error {
	state := cb.getState()

	switch state {
	case StateOpen:
		return cb.handleOpen(fn)
	case StateHalfOpen:
		return cb.handleHalfOpen(fn)
	case StateClosed:
		return cb.handleClosed(fn)
	default:
		return cb.handleClosed(fn)
	}
}

func (cb *CircuitBreaker) getState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// handleClosed implements the strategy for the CLOSED state.
// It allows all requests and transitions to OPEN if failures exceed maxFailures.
func (cb *CircuitBreaker) handleClosed(fn func() error) error {
	err := fn()
	cb.recordResult(err)
	return err
}

// handleOpen implements the strategy for the OPEN state.
// It rejects requests until the reset timeout has passed, then transitions to HALF-OPEN.
func (cb *CircuitBreaker) handleOpen(fn func() error) error {
	cb.mu.Lock()
	if time.Since(cb.lastFailureTime) <= cb.resetTimeout {
		cb.mu.Unlock()
		return errors.New("circuit breaker is open")
	}

	// Reset timeout reached, transition to Half-Open and allow trial call
	cb.state = StateHalfOpen
	cb.mu.Unlock()

	return cb.handleHalfOpen(fn)
}

// handleHalfOpen implements the strategy for the HALF-OPEN state.
// It allows trial requests. A single success closes the circuit, while a failure re-opens it.
func (cb *CircuitBreaker) handleHalfOpen(fn func() error) error {
	err := fn()
	cb.recordResult(err)
	return err
}

// recordResult handles the state transitions after a function call completes.
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailureTime = time.Now()

		if cb.failures >= cb.maxFailures {
			cb.state = StateOpen
		}
		return
	}

	// Success: if we were in Half-Open, we return to the Closed state
	if cb.state == StateHalfOpen {
		cb.state = StateClosed
	}
	cb.failures = 0
}
