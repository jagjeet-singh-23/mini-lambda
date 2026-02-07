package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher publishes messages to RabbitMQ
type Publisher struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

// NewPublisher creates a new RabbitMQ publisher
func NewPublisher(amqpURL string) (*Publisher, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	return &Publisher{
		conn:    conn,
		channel: ch,
	}, nil
}

// DeclareQueue declares a queue (idempotent)
func (p *Publisher) DeclareQueue(queueName string) error {
	_, err := p.channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	return err
}

// Publish publishes a message to a queue
func (p *Publisher) Publish(ctx context.Context, queueName string, message interface{}) error {
	// Ensure queue exists
	if err := p.DeclareQueue(queueName); err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	// Marshal message to JSON
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Publish message
	err = p.channel.PublishWithContext(
		ctx,
		"",        // exchange
		queueName, // routing key
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
			Timestamp:    time.Now(),
		},
	)

	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	logger.Info("Published message to queue", "queue", queueName)
	return nil
}

// Close closes the publisher connection
func (p *Publisher) Close() error {
	if p.channel != nil {
		p.channel.Close()
	}
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

// Consumer consumes messages from RabbitMQ
type Consumer struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

// NewConsumer creates a new RabbitMQ consumer
func NewConsumer(amqpURL string) (*Consumer, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	// Set QoS to process one message at a time
	err = ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to set QoS: %w", err)
	}

	return &Consumer{
		conn:    conn,
		channel: ch,
	}, nil
}

// DeclareQueue declares a queue (idempotent)
func (c *Consumer) DeclareQueue(queueName string) error {
	_, err := c.channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	return err
}

// Consume consumes messages from a queue
func (c *Consumer) Consume(queueName string, handler func([]byte) error) error {
	// Ensure queue exists
	if err := c.DeclareQueue(queueName); err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	// Start consuming
	msgs, err := c.channel.Consume(
		queueName, // queue
		"",        // consumer tag
		false,     // auto-ack
		false,     // exclusive
		false,     // no-local
		false,     // no-wait
		nil,       // args
	)
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	logger.Info("Started consuming from queue", "queue", queueName)

	// Process messages
	for msg := range msgs {
		logger.Info("Received message", "queue", queueName)

		err := handler(msg.Body)
		if err != nil {
			logger.Error("Failed to process message", "error", err)
			// Reject and requeue
			msg.Nack(false, true)
		} else {
			// Acknowledge
			msg.Ack(false)
		}
	}

	return nil
}

// Close closes the consumer connection
func (c *Consumer) Close() error {
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
