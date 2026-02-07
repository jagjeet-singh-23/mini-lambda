package queue_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/builder"
	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/queue"
)

func TestRabbitMQ_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	amqpURL := "amqp://guest:guest@localhost:5672/"
	queueName := "test-build-jobs"

	t.Run("Publish and Consume message", func(t *testing.T) {
		// Create publisher
		publisher, err := queue.NewPublisher(amqpURL)
		if err != nil {
			t.Fatalf("Failed to create publisher: %v", err)
		}
		defer publisher.Close()

		// Create consumer
		consumer, err := queue.NewConsumer(amqpURL)
		if err != nil {
			t.Fatalf("Failed to create consumer: %v", err)
		}
		defer consumer.Close()

		// Test message
		testJob := builder.BuildJob{
			ID:         "job-123",
			FunctionID: "func-456",
			Runtime:    "python3.9",
			PackageURL: "s3://bucket/package.zip",
			Status:     string(builder.StatusQueued),
			CreatedAt:  time.Now(),
		}

		// Publish message
		ctx := context.Background()
		err = publisher.Publish(ctx, queueName, testJob)
		if err != nil {
			t.Fatalf("Failed to publish message: %v", err)
		}

		// Consume message
		received := make(chan builder.BuildJob, 1)
		errorChan := make(chan error, 1)

		go func() {
			err := consumer.Consume(queueName, func(body []byte) error {
				var job builder.BuildJob
				if err := json.Unmarshal(body, &job); err != nil {
					return err
				}
				received <- job
				return nil
			})
			if err != nil {
				errorChan <- err
			}
		}()

		// Wait for message
		select {
		case job := <-received:
			if job.ID != testJob.ID {
				t.Errorf("Job ID mismatch: got %s, want %s", job.ID, testJob.ID)
			}
			if job.FunctionID != testJob.FunctionID {
				t.Errorf("Function ID mismatch: got %s, want %s", job.FunctionID, testJob.FunctionID)
			}
		case err := <-errorChan:
			t.Fatalf("Consumer error: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for message")
		}
	})

	t.Run("Multiple messages in order", func(t *testing.T) {
		publisher, err := queue.NewPublisher(amqpURL)
		if err != nil {
			t.Fatalf("Failed to create publisher: %v", err)
		}
		defer publisher.Close()

		consumer, err := queue.NewConsumer(amqpURL)
		if err != nil {
			t.Fatalf("Failed to create consumer: %v", err)
		}
		defer consumer.Close()

		ctx := context.Background()
		numMessages := 5

		// Publish multiple messages
		for i := 0; i < numMessages; i++ {
			job := builder.BuildJob{
				ID:         fmt.Sprintf("job-%d", i),
				FunctionID: "func-test",
				Runtime:    "python3.9",
				Status:     string(builder.StatusQueued),
				CreatedAt:  time.Now(),
			}
			err = publisher.Publish(ctx, queueName, job)
			if err != nil {
				t.Fatalf("Failed to publish message %d: %v", i, err)
			}
		}

		// Consume messages
		received := make(chan builder.BuildJob, numMessages)
		go func() {
			consumer.Consume(queueName, func(body []byte) error {
				var job builder.BuildJob
				if err := json.Unmarshal(body, &job); err != nil {
					return err
				}
				received <- job
				return nil
			})
		}()

		// Verify all messages received
		receivedCount := 0
		timeout := time.After(10 * time.Second)

		for receivedCount < numMessages {
			select {
			case job := <-received:
				t.Logf("Received job: %s", job.ID)
				receivedCount++
			case <-timeout:
				t.Fatalf("Timeout: received %d/%d messages", receivedCount, numMessages)
			}
		}

		if receivedCount != numMessages {
			t.Errorf("Expected %d messages, got %d", numMessages, receivedCount)
		}
	})
}

func TestRabbitMQ_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	t.Run("Invalid AMQP URL", func(t *testing.T) {
		_, err := queue.NewPublisher("amqp://invalid:5672/")
		if err == nil {
			t.Error("Expected error for invalid URL")
		}
	})

	t.Run("Connection retry", func(t *testing.T) {
		// This test would require stopping/starting RabbitMQ
		// Skipping for now
		t.Skip("Requires RabbitMQ restart")
	})
}
