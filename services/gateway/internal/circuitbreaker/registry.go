package circuitbreaker

import (
	"fmt"
	"sync"
	"time"
)

// Registry manages circuit breakers for multiple downstream services
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

// NewRegistry creates a new circuit breaker registry
func NewRegistry() *Registry {
	return &Registry{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// Register creates and registers a new circuit breaker for a service
func (r *Registry) Register(serviceName string, maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	cb := NewCircuitBreaker(maxFailures, resetTimeout)
	r.breakers[serviceName] = cb
	return cb
}

// Get retrieves a circuit breaker by service name
func (r *Registry) Get(serviceName string) (*CircuitBreaker, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cb, exists := r.breakers[serviceName]
	if !exists {
		return nil, fmt.Errorf("circuit breaker not found for service: %s", serviceName)
	}
	return cb, nil
}
