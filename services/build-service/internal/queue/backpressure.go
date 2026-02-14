package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/jagjeet-singh-23/mini-lambda/shared/metrics"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	MaxQueueDepth      = 100
	QueueDepthWarning  = 50
	QueueDepthCritical = 80
)

type BackpressureLevel int

const (
	BackpressureNone BackpressureLevel = iota
	BackpressureWarning
	BackpressureCritical
	BackpressureFull
)

type BackpressureManager struct {
	conn      *amqp.Connection
	queueName string
}

func NewBackpressureManager(conn *amqp.Connection, queueName string) *BackpressureManager {
	return &BackpressureManager{
		conn:      conn,
		queueName: queueName,
	}
}

type thresholdInfo struct {
	val     int
	label   string
	logMsg  string
	userMsg string
	level   BackpressureLevel
}

var backpressureThresholds = []thresholdInfo{
	{MaxQueueDepth, "queue_full", "full", "at capacity", BackpressureFull},
	{QueueDepthCritical, "queue_critical", "at critical level", "at critical level", BackpressureCritical},
	{QueueDepthWarning, "queue_warning", "at warning level", "at warning level", BackpressureWarning},
}

// GetQueueDepth returns the current depth of the queue
func (b *BackpressureManager) GetQueueDepth(ctx context.Context) (int, error) {
	ch, err := b.conn.Channel()
	if err != nil {
		return 0, err
	}

	defer ch.Close()

	queue, err := ch.QueueDeclarePassive(
		b.queueName, // name
		false,       // durable
		false,       // auto-delete
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	if err != nil {
		return 0, err
	}

	return queue.Messages, nil
}

// CanAcceptRequest checks if the queue can accept a new request
func (b *BackpressureManager) CanAcceptRequest(ctx context.Context) (bool, string, error) {
	depth, err := b.GetQueueDepth(ctx)
	if err != nil {
		logger.Error("Failed to get queue depth", "error", err.Error())
		return false, "", err
	}

	// Record metric
	metrics.QueueDepth.WithLabelValues(b.queueName).Set(float64(depth))

	for _, t := range backpressureThresholds {
		if depth >= t.val {
			metrics.BackpressureRejectionsTotal.WithLabelValues("build-service", t.label).Inc()
			logger.Warn(fmt.Sprintf("Queue is %s, rejecting request", t.logMsg), "queue", b.queueName, "depth", depth)
			return false, fmt.Sprintf("Queue %s (%d/%d). Please retry later.", t.userMsg, depth, MaxQueueDepth), nil
		}
	}

	return true, "", nil
}

// GetBackpressureLevel returns current backpressure level
func (b *BackpressureManager) GetBackpressureLevel(ctx context.Context) (BackpressureLevel, int, error) {
	depth, err := b.GetQueueDepth(ctx)
	if err != nil {
		return BackpressureNone, 0, err
	}

	for _, t := range backpressureThresholds {
		if depth >= t.val {
			return t.level, depth, nil
		}
	}

	return BackpressureNone, depth, nil
}

// MonitorQueueDepth monitors the queue depth and logs warnings when it exceeds thresholds
func (b *BackpressureManager) MonitorQueueDepth(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	logger.Info("Starting queue depth monitor", "queue", b.queueName, "interval", "10s")

	for range ticker.C {
		depth, err := b.GetQueueDepth(ctx)
		if err != nil {
			logger.Error("Failed to get queue depth", "error", err.Error())
			continue
		}

		metrics.QueueDepth.WithLabelValues(b.queueName).Set(float64(depth))

		for _, t := range backpressureThresholds {
			if depth >= t.val {
				logger.Warn(fmt.Sprintf("Queue depth is %s", t.logMsg), "queue", b.queueName, "depth", depth)
				break // Only log the most severe threshold
			}
		}
	}
}

// EstimateWaitTime estimates the time a request will wait in the queue
func (b *BackpressureManager) EstimateWaitTime(queueDepth int) int {
	if queueDepth < 0 {
		return 0
	}

	// Average build time: 2minutes
	// Average workers: 3
	// Throughput: ~1.5 builds per minute

	const throughput = 1.5
	return int(float64(queueDepth) / throughput)
}
