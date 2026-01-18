package metrics

import (
	"net/http"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/pool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsCollector manages all Prometheus metrics for mini-lambda
type MetricsCollector struct {
	// Pool metrics
	poolSize            *prometheus.GaugeVec
	poolWarmContainers  *prometheus.GaugeVec
	poolInUseContainers *prometheus.GaugeVec
	poolHitRate         *prometheus.GaugeVec
	poolColdStarts      *prometheus.CounterVec
	poolWarmStarts      *prometheus.CounterVec
	poolEvictions       *prometheus.CounterVec
	poolAvgUseCount     *prometheus.GaugeVec

	// Execution metrics
	executionsTotal       *prometheus.CounterVec
	executionDuration     *prometheus.HistogramVec
	executionPoolAcquire  *prometheus.HistogramVec
	executionCodeDuration *prometheus.HistogramVec
	executionOutputRead   *prometheus.HistogramVec

	registry *prometheus.Registry
}

// NewMetricsCollector creates and registers all Prometheus metrics
func NewMetricsCollector() *MetricsCollector {
	registry := prometheus.NewRegistry()

	mc := &MetricsCollector{
		poolSize: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "mini_lambda_pool_size",
				Help: "Current number of containers in the pool",
			},
			[]string{"runtime"},
		),
		poolWarmContainers: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "mini_lambda_pool_warm_containers",
				Help: "Number of warm (available) containers in the pool",
			},
			[]string{"runtime"},
		),
		poolInUseContainers: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "mini_lambda_pool_in_use_containers",
				Help: "Number of containers currently in use",
			},
			[]string{"runtime"},
		),
		poolHitRate: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "mini_lambda_pool_hit_rate",
				Help: "Pool hit rate percentage (0-100)",
			},
			[]string{"runtime"},
		),
		poolColdStarts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "mini_lambda_pool_cold_starts_total",
				Help: "Total number of cold starts (new container creations)",
			},
			[]string{"runtime"},
		),
		poolWarmStarts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "mini_lambda_pool_warm_starts_total",
				Help: "Total number of warm starts (container reuse)",
			},
			[]string{"runtime"},
		),
		poolEvictions: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "mini_lambda_pool_evictions_total",
				Help: "Total number of container evictions",
			},
			[]string{"runtime"},
		),
		poolAvgUseCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "mini_lambda_pool_average_use_count",
				Help: "Average number of times containers have been reused",
			},
			[]string{"runtime"},
		),
		executionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "mini_lambda_executions_total",
				Help: "Total number of function executions",
			},
			[]string{"runtime", "status"},
		),
		executionDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "mini_lambda_execution_duration_seconds",
				Help:    "Function execution duration in seconds",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
			},
			[]string{"runtime", "warm_start"},
		),
		executionPoolAcquire: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "mini_lambda_execution_pool_acquire_seconds",
				Help:    "Time to acquire container from pool in seconds",
				Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10),
			},
			[]string{"runtime"},
		),
		executionCodeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "mini_lambda_execution_code_duration_seconds",
				Help:    "Actual code execution time in seconds",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
			},
			[]string{"runtime"},
		),
		executionOutputRead: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "mini_lambda_execution_output_read_seconds",
				Help:    "Time to read execution output in seconds",
				Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10),
			},
			[]string{"runtime"},
		),
		registry: registry,
	}

	// Register all metrics
	registry.MustRegister(
		mc.poolSize,
		mc.poolWarmContainers,
		mc.poolInUseContainers,
		mc.poolHitRate,
		mc.poolColdStarts,
		mc.poolWarmStarts,
		mc.poolEvictions,
		mc.poolAvgUseCount,
		mc.executionsTotal,
		mc.executionDuration,
		mc.executionPoolAcquire,
		mc.executionCodeDuration,
		mc.executionOutputRead,
	)

	return mc
}

// RecordPoolStats updates pool metrics from pool statistics
func (mc *MetricsCollector) RecordPoolStats(
	runtime string,
	stats pool.PoolStats,
) {
	mc.poolSize.WithLabelValues(runtime).Set(float64(stats.TotalContainers))
	mc.poolWarmContainers.WithLabelValues(runtime).Set(
		float64(stats.WarmContainers),
	)
	mc.poolInUseContainers.WithLabelValues(runtime).Set(
		float64(stats.InUseContainers),
	)
	mc.poolHitRate.WithLabelValues(runtime).Set(stats.HitRate)
	mc.poolAvgUseCount.WithLabelValues(runtime).Set(stats.AverageUseCount)

	// Set counters to absolute values
	mc.poolColdStarts.WithLabelValues(runtime).Add(0)
	mc.poolWarmStarts.WithLabelValues(runtime).Add(0)
	mc.poolEvictions.WithLabelValues(runtime).Add(0)
}

// RecordColdStart increments cold start counter
func (mc *MetricsCollector) RecordColdStart(runtime string) {
	mc.poolColdStarts.WithLabelValues(runtime).Inc()
}

// RecordWarmStart increments warm start counter
func (mc *MetricsCollector) RecordWarmStart(runtime string) {
	mc.poolWarmStarts.WithLabelValues(runtime).Inc()
}

// RecordEviction increments eviction counter
func (mc *MetricsCollector) RecordEviction(runtime string) {
	mc.poolEvictions.WithLabelValues(runtime).Inc()
}

// RecordExecution records execution metrics
func (mc *MetricsCollector) RecordExecution(
	runtime string,
	duration time.Duration,
	warmStart bool,
	status string,
) {
	mc.executionsTotal.WithLabelValues(runtime, status).Inc()

	warmStartLabel := "false"
	if warmStart {
		warmStartLabel = "true"
	}

	mc.executionDuration.WithLabelValues(
		runtime,
		warmStartLabel,
	).Observe(duration.Seconds())
}

// RecordPoolAcquireTime records time to acquire container from pool
func (mc *MetricsCollector) RecordPoolAcquireTime(
	runtime string,
	duration time.Duration,
) {
	mc.executionPoolAcquire.WithLabelValues(runtime).Observe(
		duration.Seconds(),
	)
}

// RecordCodeExecutionTime records actual code execution time
func (mc *MetricsCollector) RecordCodeExecutionTime(
	runtime string,
	duration time.Duration,
) {
	mc.executionCodeDuration.WithLabelValues(runtime).Observe(
		duration.Seconds(),
	)
}

// RecordOutputReadTime records time to read execution output
func (mc *MetricsCollector) RecordOutputReadTime(
	runtime string,
	duration time.Duration,
) {
	mc.executionOutputRead.WithLabelValues(runtime).Observe(
		duration.Seconds(),
	)
}

// Handler returns an HTTP handler for the /metrics endpoint
func (mc *MetricsCollector) Handler() http.Handler {
	return promhttp.HandlerFor(mc.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
