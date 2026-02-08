package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP Metrics
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"service", "method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "method", "path"},
	)

	// Function Metrics
	FunctionInvocationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "function_invocations_total",
			Help: "Total number of function invocations",
		},
		[]string{"function_id", "function_name", "status"},
	)

	FunctionExecutionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "function_execution_duration_seconds",
			Help:    "Function execution duration in seconds",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10, 30},
		},
		[]string{"function_id", "function_name"},
	)

	FunctionColdStarts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "function_cold_starts_total",
			Help: "Total number of cold starts",
		},
		[]string{"function_id", "function_name"},
	)

	// Build Metrics
	BuildsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "builds_total",
			Help: "Total number of builds",
		},
		[]string{"runtime", "status"},
	)

	BuildDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "build_duration_seconds",
			Help:    "Build duration in seconds",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
		},
		[]string{"runtime"},
	)

	// Queue Metrics
	QueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "queue_depth",
			Help: "Current queue depth",
		},
		[]string{"queue_name"},
	)

	QueueProcessingTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "queue_processing_time_seconds",
			Help:    "Time to process queue message",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"queue_name"},
	)

	// Database Metrics
	DatabaseQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "database_query_duration_seconds",
			Help:    "Database query duration",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
		},
		[]string{"query_type", "table"},
	)

	// Redis Metrics
	CacheHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Total cache hits",
		},
		[]string{"cache_type"},
	)

	CacheMissesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "Total cache misses",
		},
		[]string{"cache_type"},
	)
)

// RecordHTTPRequest records HTTP request metrics
func RecordHTTPRequest(service, method, path string, status int, duration float64) {
	HTTPRequestsTotal.WithLabelValues(service, method, path, fmt.Sprintf("%d", status)).Inc()
	HTTPRequestDuration.WithLabelValues(service, method, path).Observe(duration)
}

// RecordFunctionInvocation records function invocation metrics
func RecordFunctionInvocation(functionID, functionName, status string, duration float64, coldStart bool) {
	FunctionInvocationsTotal.WithLabelValues(functionID, functionName, status).Inc()
	FunctionExecutionDuration.WithLabelValues(functionID, functionName).Observe(duration)

	if coldStart {
		FunctionColdStarts.WithLabelValues(functionID, functionName).Inc()
	}
}

// RecordBuild records build metrics
func RecordBuild(runtime, status string, duration float64) {
	BuildsTotal.WithLabelValues(runtime, status).Inc()
	BuildDuration.WithLabelValues(runtime).Observe(duration)
}

// RecordQueueDepth updates queue depth
func RecordQueueDepth(queueName string, depth float64) {
	QueueDepth.WithLabelValues(queueName).Set(depth)
}

// RecordQueueProcessing records queue message processing time
func RecordQueueProcessing(queueName string, duration float64) {
	QueueProcessingTime.WithLabelValues(queueName).Observe(duration)
}
