package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	amqp "github.com/rabbitmq/amqp091-go"
)

// fakeAcknowledger is a mock amqp.Acknowledger. amqp091-go documents
// Delivery.Acknowledger as the intended seam for testing Delivery handlers
// without a real broker connection.
type fakeAcknowledger struct {
	mu      sync.Mutex
	acked   []uint64
	nacked  []uint64
	requeue []bool
}

func (f *fakeAcknowledger) Ack(tag uint64, multiple bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, tag)
	return nil
}

func (f *fakeAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nacked = append(f.nacked, tag)
	f.requeue = append(f.requeue, requeue)
	return nil
}

func (f *fakeAcknowledger) Reject(tag uint64, requeue bool) error { return nil }

// fakeProcessor lets tests control whether Process succeeds or fails.
type fakeProcessor struct {
	err error
}

func (p *fakeProcessor) Process(ctx context.Context, event *domain.Event) error {
	return p.err
}

func delivery(ack *fakeAcknowledger, body []byte) amqp.Delivery {
	return amqp.Delivery{Acknowledger: ack, DeliveryTag: 1, Body: body}
}

// TestHandleMessage_AcksOnSuccess verifies a successfully processed event is
// acked (not left on the queue or sent to the DLQ).
func TestHandleMessage_AcksOnSuccess(t *testing.T) {
	bus := &RabbitMQEventBus{processor: &fakeProcessor{err: nil}}
	ack := &fakeAcknowledger{}

	event := domain.Event{ID: "evt-1", Type: domain.EventTypeHTTP, FunctionID: "fn-1"}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	bus.handleMessage(context.Background(), delivery(ack, body))

	ack.mu.Lock()
	defer ack.mu.Unlock()
	if len(ack.acked) != 1 {
		t.Fatalf("acked = %v, want exactly one ack", ack.acked)
	}
	if len(ack.nacked) != 0 {
		t.Fatalf("nacked = %v, want none", ack.nacked)
	}
}

// TestHandleMessage_RequeuesWhenRetriesRemain verifies a failed event with
// retries remaining is nacked with requeue=true.
func TestHandleMessage_RequeuesWhenRetriesRemain(t *testing.T) {
	bus := &RabbitMQEventBus{processor: &fakeProcessor{err: context.DeadlineExceeded}}
	ack := &fakeAcknowledger{}

	event := domain.Event{ID: "evt-2", RetryCount: 0, MaxRetries: 3}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	bus.handleMessage(context.Background(), delivery(ack, body))

	ack.mu.Lock()
	defer ack.mu.Unlock()
	if len(ack.nacked) != 1 || ack.requeue[0] != true {
		t.Fatalf("nacked=%v requeue=%v, want one nack with requeue=true", ack.nacked, ack.requeue)
	}
}

// TestHandleMessage_SendsToDLQWhenRetriesExhausted verifies a failed event
// with no retries left is nacked with requeue=false, routing it to the DLQ.
func TestHandleMessage_SendsToDLQWhenRetriesExhausted(t *testing.T) {
	bus := &RabbitMQEventBus{processor: &fakeProcessor{err: context.DeadlineExceeded}}
	ack := &fakeAcknowledger{}

	event := domain.Event{ID: "evt-3", RetryCount: 3, MaxRetries: 3}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	bus.handleMessage(context.Background(), delivery(ack, body))

	ack.mu.Lock()
	defer ack.mu.Unlock()
	if len(ack.nacked) != 1 || ack.requeue[0] != false {
		t.Fatalf("nacked=%v requeue=%v, want one nack with requeue=false", ack.nacked, ack.requeue)
	}
}
