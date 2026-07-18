package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	sharedqueue "github.com/jagjeet-singh-23/mini-lambda/shared/queue"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher publishes messages to RabbitMQ.
//
// If the underlying connection drops (broker restart, network blip, etc.)
// Publisher automatically redials in the background using a decorrelated
// jitter backoff (base=500ms, cap=30s) so callers don't need to notice or
// restart the process — Publish just keeps working once the reconnect
// completes.
type Publisher struct {
	amqpURL string

	mu      sync.RWMutex
	conn    *amqp.Connection
	channel *amqp.Channel

	backoff *sharedqueue.Backoff
	ctx     context.Context
	cancel  context.CancelFunc
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

	ctx, cancel := context.WithCancel(context.Background())
	p := &Publisher{
		amqpURL: amqpURL,
		conn:    conn,
		channel: ch,
		backoff: sharedqueue.NewBackoff(),
		ctx:     ctx,
		cancel:  cancel,
	}

	go p.reconnectLoop(conn)

	return p, nil
}

// reconnectLoop watches the given connection for closure and, once it
// closes, redials (with backoff) and installs the fresh connection/channel.
// It then repeats, watching the new connection, until the Publisher is
// closed.
func (p *Publisher) reconnectLoop(conn *amqp.Connection) {
	for {
		notifyClose := conn.NotifyClose(make(chan *amqp.Error, 1))

		select {
		case <-p.ctx.Done():
			return
		case err := <-notifyClose:
			logger.Warn("Publisher connection to RabbitMQ closed, reconnecting", "error", errString(err))
		}

		newConn, ok := p.redial()
		if !ok {
			// Publisher was closed while we were trying to reconnect.
			return
		}
		conn = newConn
	}
}

// redial retries dialing RabbitMQ and opening a channel using decorrelated
// jitter backoff until it succeeds or the Publisher is closed. On success it
// swaps in the new connection/channel and resets the backoff state.
func (p *Publisher) redial() (*amqp.Connection, bool) {
	for {
		select {
		case <-p.ctx.Done():
			return nil, false
		default:
		}

		conn, err := amqp.Dial(p.amqpURL)
		if err == nil {
			var ch *amqp.Channel
			ch, err = conn.Channel()
			if err == nil {
				p.mu.Lock()
				p.conn = conn
				p.channel = ch
				p.mu.Unlock()

				p.backoff.Reset()
				logger.Info("Publisher reconnected to RabbitMQ")
				return conn, true
			}
			conn.Close()
		}

		logger.Error("Publisher failed to reconnect to RabbitMQ, retrying", "error", err)
		if !p.sleepBackoff() {
			return nil, false
		}
	}
}

func (p *Publisher) sleepBackoff() bool {
	d := p.backoff.Next()
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-p.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *Publisher) getChannel() *amqp.Channel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.channel
}

// DeclareQueue declares a queue (idempotent)
func (p *Publisher) DeclareQueue(queueName string) error {
	_, err := p.getChannel().QueueDeclare(
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
	err = p.getChannel().PublishWithContext(
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

// Close closes the publisher connection and stops the background reconnect loop.
func (p *Publisher) Close() error {
	p.cancel()

	p.mu.RLock()
	ch, conn := p.channel, p.conn
	p.mu.RUnlock()

	if ch != nil {
		ch.Close()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// GetConnection returns the underlying AMQP connection currently in use.
// Note: since Publisher may transparently redial after this method is
// called, callers that hold onto the returned pointer across a potential
// reconnect (rather than using it immediately) will not see subsequent
// reconnects reflected.
func (p *Publisher) GetConnection() *amqp.Connection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.conn
}

// Consumer consumes messages from RabbitMQ.
//
// If the underlying connection drops, Consume automatically redials (with
// decorrelated jitter backoff, base=500ms/cap=30s), re-declares the queue,
// and re-registers the consumer on the fresh channel — a dropped connection
// resumes processing on its own instead of requiring a manual restart.
type Consumer struct {
	amqpURL string

	mu      sync.RWMutex
	conn    *amqp.Connection
	channel *amqp.Channel

	backoff *sharedqueue.Backoff
	ctx     context.Context
	cancel  context.CancelFunc
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

	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		amqpURL: amqpURL,
		conn:    conn,
		channel: ch,
		backoff: sharedqueue.NewBackoff(),
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

func (c *Consumer) getChannel() *amqp.Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.channel
}

func (c *Consumer) getConn() *amqp.Connection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

// DeclareQueue declares a queue (idempotent)
func (c *Consumer) DeclareQueue(queueName string) error {
	_, err := c.getChannel().QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	return err
}

// Consume consumes messages from a queue. It blocks until the consumer is
// closed (via Close), automatically reconnecting to RabbitMQ and resuming
// message processing whenever the connection drops.
func (c *Consumer) Consume(queueName string, handler func([]byte) error) error {
	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
		}

		msgs, err := c.startConsuming(queueName)
		if err != nil {
			logger.Error("Failed to start consuming, will retry", "queue", queueName, "error", err)
			if !c.sleepBackoff() {
				return nil
			}
			continue
		}

		// A fresh connection means a fresh backoff budget for the *next* failure.
		c.backoff.Reset()
		logger.Info("Started consuming from queue", "queue", queueName)

		notifyClose := c.getConn().NotifyClose(make(chan *amqp.Error, 1))
		c.processMessages(queueName, msgs, notifyClose, handler)

		select {
		case <-c.ctx.Done():
			return nil
		default:
		}

		logger.Warn("RabbitMQ connection lost, reconnecting", "queue", queueName)
		if !c.reconnect() {
			return nil
		}
	}
}

// startConsuming (re-)declares the queue and registers a fresh Consume() on
// the current channel. A fresh connection needs a fresh channel and a fresh
// consumer registration — the old ones die with the connection.
func (c *Consumer) startConsuming(queueName string) (<-chan amqp.Delivery, error) {
	if err := c.DeclareQueue(queueName); err != nil {
		return nil, fmt.Errorf("failed to declare queue: %w", err)
	}

	msgs, err := c.getChannel().Consume(
		queueName, // queue
		"",        // consumer tag
		false,     // auto-ack
		false,     // exclusive
		false,     // no-local
		false,     // no-wait
		nil,       // args
	)
	if err != nil {
		return nil, fmt.Errorf("failed to register consumer: %w", err)
	}

	return msgs, nil
}

// processMessages drains msgs, acking/nacking each one, until either the
// consumer is closed, the connection reports a close event, or the delivery
// channel itself closes (both happen when the broker connection drops).
func (c *Consumer) processMessages(
	queueName string,
	msgs <-chan amqp.Delivery,
	notifyClose <-chan *amqp.Error,
	handler func([]byte) error,
) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case err := <-notifyClose:
			if err != nil {
				logger.Error("RabbitMQ connection closed", "error", err.Error())
			}
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}

			logger.Info("Received message", "queue", queueName)

			if err := handler(msg.Body); err != nil {
				logger.Error("Failed to process message", "error", err)
				msg.Nack(false, false) // discard — build failures won't self-heal on retry
			} else {
				msg.Ack(false)
			}
		}
	}
}

// reconnect closes out the dead connection/channel and redials using
// decorrelated jitter backoff until it succeeds or the consumer is closed.
func (c *Consumer) reconnect() bool {
	c.mu.Lock()
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()

	for {
		select {
		case <-c.ctx.Done():
			return false
		default:
		}

		conn, err := amqp.Dial(c.amqpURL)
		if err == nil {
			var ch *amqp.Channel
			ch, err = conn.Channel()
			if err == nil {
				if qosErr := ch.Qos(1, 0, false); qosErr == nil {
					c.mu.Lock()
					c.conn = conn
					c.channel = ch
					c.mu.Unlock()
					logger.Info("Consumer reconnected to RabbitMQ")
					return true
				} else {
					err = qosErr
				}
				ch.Close()
			}
			conn.Close()
		}

		logger.Error("Failed to reconnect to RabbitMQ, retrying", "error", err)
		if !c.sleepBackoff() {
			return false
		}
	}
}

func (c *Consumer) sleepBackoff() bool {
	d := c.backoff.Next()
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-c.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Close closes the consumer connection and stops any in-flight reconnect loop.
func (c *Consumer) Close() error {
	c.cancel()

	c.mu.RLock()
	ch, conn := c.channel, c.conn
	c.mu.RUnlock()

	if ch != nil {
		ch.Close()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func errString(err *amqp.Error) string {
	if err == nil {
		return "connection closed (no error detail)"
	}
	return err.Error()
}
