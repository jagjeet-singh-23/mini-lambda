package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// fakeAcknowledger is a mock amqp.Acknowledger — the amqp091-go package
// itself documents Delivery.Acknowledger as the intended seam for testing
// Delivery handlers without a real broker connection.
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

func (f *fakeAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

// TestProcessMessages_AcksOnSuccessNacksOnError verifies the per-message
// ack/nack behavior that must survive across a reconnect (it's the same
// helper reused for every generation of the delivery channel).
func TestProcessMessages_AcksOnSuccessNacksOnError(t *testing.T) {
	c := &Consumer{ctx: context.Background()}

	ack := &fakeAcknowledger{}
	msgs := make(chan amqp.Delivery, 2)
	msgs <- amqp.Delivery{Acknowledger: ack, DeliveryTag: 1, Body: []byte("ok")}
	msgs <- amqp.Delivery{Acknowledger: ack, DeliveryTag: 2, Body: []byte("bad")}
	close(msgs)

	notifyClose := make(chan *amqp.Error)

	var handled []string
	handler := func(body []byte) error {
		handled = append(handled, string(body))
		if string(body) == "bad" {
			return errors.New("processing failed")
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		c.processMessages("test-queue", msgs, notifyClose, handler)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("processMessages did not return after msgs channel closed")
	}

	if len(handled) != 2 || handled[0] != "ok" || handled[1] != "bad" {
		t.Fatalf("handler calls = %v, want [ok bad]", handled)
	}

	ack.mu.Lock()
	defer ack.mu.Unlock()
	if len(ack.acked) != 1 || ack.acked[0] != 1 {
		t.Fatalf("acked = %v, want [1]", ack.acked)
	}
	if len(ack.nacked) != 1 || ack.nacked[0] != 2 {
		t.Fatalf("nacked = %v, want [2]", ack.nacked)
	}
	if len(ack.requeue) != 1 || ack.requeue[0] != false {
		t.Fatalf("nack requeue = %v, want [false] (build failures don't self-heal on retry)", ack.requeue)
	}
}

// TestProcessMessages_ReturnsOnNotifyClose verifies that a connection-close
// notification stops message processing promptly (this is what lets the
// Consume loop above notice the drop and kick off a reconnect+redeclare+
// re-consume, rather than blocking forever on a channel that will never
// produce another delivery).
func TestProcessMessages_ReturnsOnNotifyClose(t *testing.T) {
	c := &Consumer{ctx: context.Background()}

	msgs := make(chan amqp.Delivery) // never produces or closes on its own
	notifyClose := make(chan *amqp.Error, 1)
	notifyClose <- &amqp.Error{Code: 320, Reason: "CONNECTION_FORCED"}

	handlerCalled := false
	handler := func(body []byte) error {
		handlerCalled = true
		return nil
	}

	done := make(chan struct{})
	go func() {
		c.processMessages("test-queue", msgs, notifyClose, handler)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("processMessages did not return after NotifyClose fired")
	}

	if handlerCalled {
		t.Fatal("handler should not have been called — no messages were delivered")
	}
}

// TestProcessMessages_ReturnsOnContextCancel verifies Close() (which cancels
// the Consumer's context) unblocks an in-flight processMessages call so
// Consume can return instead of leaking the goroutine forever.
func TestProcessMessages_ReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Consumer{ctx: ctx}

	msgs := make(chan amqp.Delivery)
	notifyClose := make(chan *amqp.Error)

	done := make(chan struct{})
	go func() {
		c.processMessages("test-queue", msgs, notifyClose, func([]byte) error { return nil })
		close(done)
	}()

	// Give the goroutine a moment to enter the select loop, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("processMessages did not return after context was cancelled")
	}
}
