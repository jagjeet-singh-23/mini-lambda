package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Queue metrics

	// Backpressure metrics
	BackpressureRejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "backpressure_rejections_total",
			Help: "Total requests rejected due to backpressure",
		},
		[]string{"service", "reason"},
	)

	// Rate limiting metrics
	RateLimitRejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ratelimit_rejections_total",
			Help: "Total requests rejected due to rate limiting",
		},
		[]string{"type", "scope"},
	)

	// Circuit breaker metrics
	CircuitBreakerStateGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"name", "state"},
	)

	CircuitBreakerFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "circuit_breaker_failures_total",
			Help: "Total circuit breaker failures",
		},
		[]string{"name"},
	)

	CircuitBreakerSuccessesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "circuit_breaker_successes_total",
			Help: "Total circuit breaker successes",
		},
		[]string{"name"},
	)

	CircuitBreakerRejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "circuit_breaker_rejections_total",
			Help: "Total requests rejected by circuit breaker",
		},
		[]string{"name"},
	)
)
