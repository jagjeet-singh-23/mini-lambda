package queue

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TestConsumer_ReconnectsAfterBrokerRestart is an integration test that spins
// up a real RabbitMQ broker in Docker, establishes a Consumer against it,
// force-restarts the broker (simulating a crash/restart — the exact failure
// mode this feature exists for), and verifies the Consumer automatically
// reconnects and resumes processing messages without any manual
// intervention. It mirrors the real-infra style of
// TestExecute_TimeoutKillsContainer in
// services/lambda-service/internal/executor/timeout_test.go, which also
// drives real Docker rather than mocks.
func TestConsumer_ReconnectsAfterBrokerRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available: " + err.Error())
	}

	containerName := fmt.Sprintf("mini-lambda-reconnect-test-%d", time.Now().UnixNano())

	// Use a fixed host port rather than a Docker-assigned one: on restart,
	// Docker Desktop's port-forwarding layer can reassign an ephemeral
	// mapped port to a new number, which would make the test's own use of
	// `docker restart` indistinguishable from "the broker moved to a new
	// address" — not the scenario this feature handles. Production
	// RabbitMQ connections point at a stable host:port (or k8s Service
	// DNS name) that doesn't change when the broker process restarts, so
	// a fixed port here is the faithful analogue.
	hostPort := 25670 + (int(time.Now().UnixNano() % 1000))
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

	// Wait for the broker to actually accept AMQP connections (the
	// container can be "up" before the AMQP listener is ready).
	waitForAMQP(t, amqpURL, 60*time.Second)

	const queueName = "reconnect-test-queue"

	consumer, err := NewConsumer(amqpURL)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	received := make(chan string, 10)
	go func() {
		err := consumer.Consume(queueName, func(body []byte) error {
			received <- string(body)
			return nil
		})
		if err != nil {
			t.Logf("Consume returned: %v", err)
		}
	}()

	// Give the consumer a moment to declare the queue and register.
	waitForQueueConsumer(t, amqpURL, queueName, 10*time.Second)

	publishRaw(t, amqpURL, queueName, "message-before-restart")

	select {
	case msg := <-received:
		if msg != "message-before-restart" {
			t.Fatalf("got %q, want %q", msg, "message-before-restart")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for message before restart")
	}

	// Simulate the broker restarting out from under the consumer: this
	// drops the TCP connection, which is exactly the failure mode this
	// feature (reconnect + resumed processing) exists to handle.
	t.Log("restarting broker container to simulate a drop...")
	if out, err := exec.Command("docker", "restart", "-t", "1", containerName).CombinedOutput(); err != nil {
		t.Fatalf("docker restart: %v: %s", err, out)
	}

	waitForAMQP(t, amqpURL, 60*time.Second)

	// Publish a second message only once the broker is reachable again;
	// the Consumer's own reconnect loop is racing to redial concurrently,
	// which is exactly the scenario under test.
	publishWithRetry(t, amqpURL, queueName, "message-after-restart", 30*time.Second)

	select {
	case msg := <-received:
		if msg != "message-after-restart" {
			t.Fatalf("got %q, want %q", msg, "message-after-restart")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for message after restart — consumer did not reconnect and resume processing")
	}
}

// waitForAMQP polls until amqp.Dial succeeds or timeout elapses.
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

// waitForQueueConsumer polls until the target queue reports at least one
// consumer registered, so the test doesn't race the Consumer's own startup.
func waitForQueueConsumer(t *testing.T, amqpURL, queueName string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := amqp.Dial(amqpURL)
		if err == nil {
			ch, chErr := conn.Channel()
			if chErr == nil {
				q, qErr := ch.QueueInspect(queueName)
				ch.Close()
				conn.Close()
				if qErr == nil && q.Consumers > 0 {
					return
				}
			} else {
				conn.Close()
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("queue %q never reported a registered consumer", queueName)
}

// publishRaw sends a single persistent message directly (bypassing
// Publisher, since this test is about Consumer's reconnect behavior).
func publishRaw(t *testing.T, amqpURL, queueName, body string) {
	t.Helper()

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatalf("publishRaw dial: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("publishRaw channel: %v", err)
	}
	defer ch.Close()

	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		t.Fatalf("publishRaw declare: %v", err)
	}

	err = ch.Publish("", queueName, false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  "text/plain",
		Body:         []byte(body),
	})
	if err != nil {
		t.Fatalf("publishRaw publish: %v", err)
	}
}

// publishWithRetry retries publishRaw for up to timeout — used right after
// a broker restart, when the broker may still be finishing startup even
// though the port is open.
func publishWithRetry(t *testing.T, amqpURL, queueName, body string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr any
	for time.Now().Before(deadline) {
		conn, err := amqp.Dial(amqpURL)
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		ch, err := conn.Channel()
		if err != nil {
			conn.Close()
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
			ch.Close()
			conn.Close()
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		err = ch.Publish("", queueName, false, false, amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "text/plain",
			Body:         []byte(body),
		})
		ch.Close()
		conn.Close()
		if err == nil {
			return
		}
		lastErr = err
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("publishWithRetry: never succeeded, last error: %v", lastErr)
}
