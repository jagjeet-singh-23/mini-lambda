package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchangeName    = "mini-lambda-events"
	exchangeType    = "topic"
	dlqExchangeName = "mini-lambda-dlq"
)

type RabbitMQEventBus struct {
	conn      *amqp.Connection
	channel   *amqp.Channel
	processor EventProcessor
	consumers map[string]*consumer
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
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

	if err := ch.ExchangeDeclare(
		exchangeName,
		exchangeType,
		true,  //durable
		false, //auto-deleted
		false, //internal
		false, //no-wait
		nil,   //args
	); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	// Declare main exchange
	if err := ch.ExchangeDeclare(
		dlqExchangeName,
		exchangeType,
		true,  //durable
		false, //auto-deleted
		false, //internal
		false, //no-wait
		nil,   //args
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare DLQ exchange: %w", err)
	}

	// Declare DLQ exchange
	if err := ch.ExchangeDeclare(
		dlqExchangeName,
		"fanout",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare DLQ exchange: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bus := &RabbitMQEventBus{
		conn:      conn,
		channel:   ch,
		processor: processor,
		consumers: make(map[string]*consumer),
		ctx:       ctx,
		cancel:    cancel,
	}

	go bus.handleConnectionErrors()
	log.Println("RabbitMQ event bus initialized successfully")

	return bus, nil
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
	err = b.channel.PublishWithContext(
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

	q, err := b.channel.QueueDeclare(
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

	if err := b.channel.QueueBind(
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

	_, err := b.channel.QueueDelete(queueName, false, false, false)
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

	if b.channel != nil {
		b.channel.Close()
	}

	if b.conn != nil {
		b.conn.Close()
	}

	log.Println("Event bus shutdown complete")
	return nil
}

func (b *RabbitMQEventBus) consumeQueue(ctx context.Context, c *consumer) {
	defer b.wg.Done()

	msgs, err := b.channel.Consume(
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

func (b *RabbitMQEventBus) handleConnectionErrors() {
	notifyClose := b.conn.NotifyClose(make(chan *amqp.Error))

	for {
		select {
		case <-b.ctx.Done():
			return
		case err := <-notifyClose:
			if err != nil {
				log.Printf("Connection closed: %v", err)
			}
		}
	}
}
