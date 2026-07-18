package events

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	amqp "github.com/rabbitmq/amqp091-go"
)

// recordingProcessor records every event it processes, so the integration
// test can assert on what actually made it through the bus.
type recordingProcessor struct {
	mu       sync.Mutex
	received []string
}

func (p *recordingProcessor) Process(ctx context.Context, event *domain.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.received = append(p.received, event.ID)
	return nil
}

func (p *recordingProcessor) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.received))
	copy(out, p.received)
	return out
}

// TestRabbitMQEventBus_ReconnectsAfterBrokerRestart is an integration test
// that spins up a real RabbitMQ broker in Docker, subscribes to an event
// type, force-restarts the broker (simulating a crash/restart), and
// verifies the event bus automatically reconnects, re-declares its
// exchanges/queues/bindings, and resumes delivering events without any
// manual intervention. It mirrors the real-infra style of
// TestExecute_TimeoutKillsContainer in
// services/lambda-service/internal/executor/timeout_test.go, and the
// equivalent build-service test in
// services/build-service/internal/queue/rabbitmq_integration_test.go.
func TestRabbitMQEventBus_ReconnectsAfterBrokerRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available: " + err.Error())
	}

	containerName := fmt.Sprintf("mini-lambda-eventbus-reconnect-test-%d", time.Now().UnixNano())

	// Fixed host port: see the equivalent build-service test for why a
	// Docker-assigned ephemeral port isn't used here (it can be reassigned
	// across `docker restart` on some Docker Desktop setups, which would
	// test "the broker moved to a new address" rather than the actual
	// target scenario of a broker restarting at a stable address).
	hostPort := 25680 + (int(time.Now().UnixNano() % 1000))
	amqpURL := fmt.Sprintf("amqp://guest:guest@127.0.0.1:%d/", hostPort)

	if out, err := exec.Command("docker", "run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:5672", hostPort),
		"rabbitmq:3-alpine",
	).CombinedOutput(); err != nil {
		t.Fatalf("docker run rabbitmq: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("docker", "rm", "-f", containerName).Run()
	})

	waitForAMQP(t, amqpURL, 60*time.Second)

	processor := &recordingProcessor{}
	bus, err := NewRabbitMQEventBus(amqpURL, processor)
	if err != nil {
		t.Fatalf("NewRabbitMQEventBus: %v", err)
	}
	defer bus.Shutdown(context.Background())

	const functionID = "reconnect-test-fn"
	if err := bus.Subscribe(context.Background(), domain.EventTypeHTTP, functionID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Give the consumer goroutine a moment to register before publishing.
	time.Sleep(500 * time.Millisecond)

	if err := bus.Publish(context.Background(), &domain.Event{
		ID:         "evt-before-restart",
		Type:       domain.EventTypeHTTP,
		FunctionID: functionID,
		MaxRetries: 3,
	}); err != nil {
		t.Fatalf("Publish (before restart): %v", err)
	}

	waitForEvent(t, processor, "evt-before-restart", 10*time.Second)

	t.Log("restarting broker container to simulate a drop...")
	if out, err := exec.Command("docker", "restart", "-t", "1", containerName).CombinedOutput(); err != nil {
		t.Fatalf("docker restart: %v: %s", err, out)
	}

	waitForAMQP(t, amqpURL, 60*time.Second)

	// Publish the second event through the *same* bus instance — its
	// internal reconnect loop is racing to redial, re-declare the
	// exchanges, and resubscribe concurrently, which is exactly the
	// scenario under test. Retry the publish until the bus's own channel
	// has been swapped back in.
	publishWithRetryBus(t, bus, &domain.Event{
		ID:         "evt-after-restart",
		Type:       domain.EventTypeHTTP,
		FunctionID: functionID,
		MaxRetries: 3,
	}, 30*time.Second)

	waitForEvent(t, processor, "evt-after-restart", 60*time.Second)
}

func waitForAMQP(t *testing.T, amqpURL string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := amqp.Dial(amqpURL)
		if err == nil {
			conn.Close()
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("broker at %s never became reachable: %v", amqpURL, lastErr)
}

func waitForEvent(t *testing.T, processor *recordingProcessor, eventID string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, id := range processor.snapshot() {
			if id == eventID {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("event %q was never processed (got: %v)", eventID, processor.snapshot())
}

// publishWithRetryBus retries bus.Publish for up to timeout — used right
// after a broker restart, while the bus's own reconnect loop is still
// swapping in a fresh channel.
func publishWithRetryBus(t *testing.T, bus *RabbitMQEventBus, event *domain.Event, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := bus.Publish(context.Background(), event); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("publishWithRetryBus: never succeeded, last error: %v", lastErr)
}
