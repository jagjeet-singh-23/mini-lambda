package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	sharedqueue "github.com/jagjeet-singh-23/mini-lambda/shared/queue"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchangeName    = "mini-lambda-events"
	exchangeType    = "topic"
	dlqExchangeName = "mini-lambda-dlq"
)

// RabbitMQEventBus publishes and subscribes to domain events over RabbitMQ.
//
// If the underlying connection drops (broker restart, network blip, etc.)
// the bus automatically redials in the background using a decorrelated
// jitter backoff (base=500ms, cap=30s), re-declares the exchanges, and
// re-establishes every active subscription's queue, binding, and consumer —
// a fresh connection needs a fresh channel and fresh Consume()
// registrations, since the old ones die with the connection. Callers don't
// need to notice or restart the process for event processing to resume.
type RabbitMQEventBus struct {
	amqpURL string

	connMu  sync.RWMutex
	conn    *amqp.Connection
	channel *amqp.Channel

	processor EventProcessor
	consumers map[string]*consumer
	mu        sync.RWMutex

	backoff *sharedqueue.Backoff

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type consumer struct {
	queueName  string
	routingKey string
	cancel     context.CancelFunc
}

func NewRabbitMQEventBus(
	amqpURL string,
	processor EventProcessor,
) (*RabbitMQEventBus, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	if err := declareExchanges(ch); err != nil {
		ch.Close()
		conn.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	bus := &RabbitMQEventBus{
		amqpURL:   amqpURL,
		conn:      conn,
		channel:   ch,
		processor: processor,
		consumers: make(map[string]*consumer),
		backoff:   sharedqueue.NewBackoff(),
		ctx:       ctx,
		cancel:    cancel,
	}

	go bus.handleConnectionErrors(conn)
	log.Println("RabbitMQ event bus initialized successfully")

	return bus, nil
}

// declareExchanges declares the topic exchange used for routing events and
// the fanout DLQ exchange. It's used both on initial connect and after every
// reconnect, since a fresh connection's channel has none of this state.
func declareExchanges(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(
		exchangeName,
		exchangeType,
		true,  //durable
		false, //auto-deleted
		false, //internal
		false, //no-wait
		nil,   //args
	); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := ch.ExchangeDeclare(
		dlqExchangeName,
		"fanout",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("failed to declare DLQ exchange: %w", err)
	}

	return nil
}

func (b *RabbitMQEventBus) getChannel() *amqp.Channel {
	b.connMu.RLock()
	defer b.connMu.RUnlock()
	return b.channel
}

func (b *RabbitMQEventBus) getConn() *amqp.Connection {
	b.connMu.RLock()
	defer b.connMu.RUnlock()
	return b.conn
}

func (b *RabbitMQEventBus) Publish(
	ctx context.Context,
	event *domain.Event,
) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	routingKey := string(event.Type)
	err = b.getChannel().PublishWithContext(
		ctx,
		exchangeName,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
			MessageId:    event.ID,
			Headers: amqp.Table{
				"event_type":  string(event.Type),
				"function_id": event.FunctionID,
				"retry_count": event.RetryCount,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	log.Printf(
		"📤 Published event: id=%s type=%s function=%s",
		event.ID,
		event.Type,
		event.FunctionID,
	)
	return nil
}

func (b *RabbitMQEventBus) Subscribe(
	ctx context.Context,
	eventType domain.EventType,
	functionID string,
) error {
	queueName := fmt.Sprintf("function.%s%s", functionID, eventType)
	routingKey := string(eventType)

	ch := b.getChannel()

	q, err := ch.QueueDeclare(
		queueName,
		true,  // durable,
		false, // auto-delete,
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange": dlqExchangeName,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	if err := ch.QueueBind(
		q.Name,
		routingKey,
		exchangeName,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	consumerCtx, cancel := context.WithCancel(b.ctx)
	c := &consumer{
		queueName:  q.Name,
		routingKey: routingKey,
		cancel:     cancel,
	}

	b.mu.Lock()
	b.consumers[queueName] = c
	b.mu.Unlock()

	b.wg.Add(1)
	go b.consumeQueue(consumerCtx, c)

	log.Printf(
		"✅ Subscribed to events: type=%s function=%s queue=%s",
		eventType,
		functionID,
		queueName,
	)
	return nil
}

// Unsubscribe removes a function subscription
func (b *RabbitMQEventBus) Unsubscribe(
	eventType domain.EventType,
	functionID string,
) error {
	queueName := fmt.Sprintf("function.%s%s", functionID, eventType)

	b.mu.Lock()
	c, exists := b.consumers[queueName]
	if exists {
		c.cancel()
		delete(b.consumers, queueName)
	}
	b.mu.Unlock()

	if !exists {
		return fmt.Errorf("no subscription found for queue: %s", queueName)
	}

	_, err := b.getChannel().QueueDelete(queueName, false, false, false)
	if err != nil {
		return fmt.Errorf("failed to delete queue: %w", err)
	}

	log.Printf(
		"❌ Unsubscribed from events: type=%s function=%s queue=%s",
		eventType,
		functionID,
		queueName,
	)
	return nil
}

// Start begins processing events
func (b *RabbitMQEventBus) Start(ctx context.Context) error {
	log.Println("Event bus started...")
	<-ctx.Done()
	return b.Shutdown(context.Background())
}

func (b *RabbitMQEventBus) Shutdown(ctx context.Context) error {
	log.Println("Shutting down event bus...")
	b.cancel()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All consumers stopped")
	case <-ctx.Done():
		log.Println("Shutdown timeout, forcing stop")
	}

	ch := b.getChannel()
	if ch != nil {
		ch.Close()
	}

	conn := b.getConn()
	if conn != nil {
		conn.Close()
	}

	log.Println("Event bus shutdown complete")
	return nil
}

func (b *RabbitMQEventBus) consumeQueue(ctx context.Context, c *consumer) {
	defer b.wg.Done()

	msgs, err := b.getChannel().Consume(
		c.queueName,
		"",    // consumer tag
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		log.Printf("Failed to register a consumer: %v", err)
		return
	}

	log.Printf("Started consuming from queue: %s", c.queueName)

	for {
		select {
		case <-ctx.Done():
			log.Printf("Consumer stopped for queue: %s", c.queueName)
			return
		case msg, ok := <-msgs:
			if !ok {
				log.Printf("Channel closed for queue: %s", c.queueName)
				return
			}
			b.handleMessage(ctx, msg)
		}
	}
}

func (b *RabbitMQEventBus) handleMessage(
	ctx context.Context,
	msg amqp.Delivery,
) {
	var event domain.Event
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		log.Printf("Failed to unmarshal event: %v", err)
		msg.Nack(false, false)
		return
	}

	log.Printf(
		"📥 Received event: id=%s type=%s function=%s",
		event.ID,
		event.Type,
		event.FunctionID,
	)

	if err := b.processor.Process(ctx, &event); err != nil {
		log.Printf("Failed to process event: %v", err)

		if event.RetryCount < event.MaxRetries {
			msg.Nack(false, true) // re-queue
		} else {
			msg.Nack(false, false) // send to DLQ
		}
		return
	}

	msg.Ack(false)
}

// handleConnectionErrors watches the given connection for closure and, once
// it closes, reconnects (with decorrelated jitter backoff), re-declares the
// exchanges, and re-establishes every active subscription. It then repeats,
// watching the new connection, until the bus is shut down.
func (b *RabbitMQEventBus) handleConnectionErrors(conn *amqp.Connection) {
	for {
		notifyClose := conn.NotifyClose(make(chan *amqp.Error, 1))

		select {
		case <-b.ctx.Done():
			return
		case err := <-notifyClose:
			log.Printf("RabbitMQ connection closed: %v — reconnecting", errString(err))
		}

		newConn, ok := b.reconnect()
		if !ok {
			// Bus was shut down while we were trying to reconnect.
			return
		}
		conn = newConn
	}
}

// reconnect retries dialing RabbitMQ, opening a channel, and re-declaring
// the exchanges using decorrelated jitter backoff until it succeeds or the
// bus is shut down. On success it swaps in the new connection/channel,
// re-establishes every active subscription, and resets the backoff state.
func (b *RabbitMQEventBus) reconnect() (*amqp.Connection, bool) {
	for {
		select {
		case <-b.ctx.Done():
			return nil, false
		default:
		}

		conn, err := amqp.Dial(b.amqpURL)
		if err == nil {
			var ch *amqp.Channel
			ch, err = conn.Channel()
			if err == nil {
				if declErr := declareExchanges(ch); declErr == nil {
					b.connMu.Lock()
					b.conn = conn
					b.channel = ch
					b.connMu.Unlock()

					b.resubscribeAll(ch)
					b.backoff.Reset()
					log.Println("RabbitMQ event bus reconnected")
					return conn, true
				} else {
					err = declErr
				}
				ch.Close()
			}
			conn.Close()
		}

		log.Printf("Failed to reconnect to RabbitMQ, retrying: %v", err)
		if !b.sleepBackoff() {
			return nil, false
		}
	}
}

// resubscribeAll re-declares each currently active subscription's queue and
// binding, and starts a fresh consumeQueue goroutine for it on the given
// (freshly reconnected) channel. The old consumeQueue goroutines already
// exited when their delivery channel closed along with the dead connection.
func (b *RabbitMQEventBus) resubscribeAll(ch *amqp.Channel) {
	type entry struct {
		queueName string
		c         *consumer
	}

	b.mu.RLock()
	entries := make([]entry, 0, len(b.consumers))
	for qn, c := range b.consumers {
		entries = append(entries, entry{qn, c})
	}
	b.mu.RUnlock()

	for _, e := range entries {
		if _, err := ch.QueueDeclare(
			e.queueName,
			true,  // durable
			false, // auto-delete
			false, // exclusive
			false, // no-wait
			amqp.Table{
				"x-dead-letter-exchange": dlqExchangeName,
			},
		); err != nil {
			log.Printf("Failed to re-declare queue %s after reconnect: %v", e.queueName, err)
			continue
		}

		if err := ch.QueueBind(e.queueName, e.c.routingKey, exchangeName, false, nil); err != nil {
			log.Printf("Failed to re-bind queue %s after reconnect: %v", e.queueName, err)
			continue
		}

		consumerCtx, cancel := context.WithCancel(b.ctx)

		b.mu.Lock()
		e.c.cancel = cancel
		b.mu.Unlock()

		b.wg.Add(1)
		go b.consumeQueue(consumerCtx, e.c)

		log.Printf("Resumed consuming: queue=%s routing_key=%s", e.queueName, e.c.routingKey)
	}
}

func (b *RabbitMQEventBus) sleepBackoff() bool {
	d := b.backoff.Next()
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-b.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func errString(err *amqp.Error) string {
	if err == nil {
		return "connection closed (no error detail)"
	}
	return err.Error()
}
