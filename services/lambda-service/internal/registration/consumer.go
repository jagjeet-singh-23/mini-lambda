package registration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lsStorage "github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/storage"
	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer listens for function.built events and registers functions into
// lambda-service's own Postgres + S3 store.
type Consumer struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	s3      *lsStorage.S3Storage
	repo    domain.FunctionRepository
}

func NewConsumer(
	amqpURL string,
	s3 *lsStorage.S3Storage,
	repo domain.FunctionRepository,
) (*Consumer, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if err := ch.Qos(1, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("set QoS: %w", err)
	}

	return &Consumer{conn: conn, channel: ch, s3: s3, repo: repo}, nil
}

func (c *Consumer) Start(ctx context.Context) error {
	q, err := c.channel.QueueDeclare(
		domain.FunctionBuiltQueue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("declare queue: %w", err)
	}

	msgs, err := c.channel.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("register consumer: %w", err)
	}

	logger.Info("Registration consumer started", "queue", domain.FunctionBuiltQueue)

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return fmt.Errorf("channel closed")
			}
			if err := c.handle(ctx, msg.Body); err != nil {
				logger.Error("Function registration failed", "error", err)
				msg.Nack(false, false)
			} else {
				msg.Ack(false)
			}
		}
	}
}

func (c *Consumer) handle(ctx context.Context, body []byte) error {
	var evt domain.FunctionBuiltEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	logger.Info("Registering built function", "function_id", evt.FunctionID, "name", evt.Name)

	// Download the handler source file from S3 (path written by build-service worker)
	handlerKey := evt.S3Prefix + handlerFilename(evt.Runtime, evt.Handler)
	code, err := c.s3.RetrieveRaw(ctx, handlerKey)
	if err != nil {
		return fmt.Errorf("download handler from %s: %w", handlerKey, err)
	}

	// Re-store under blake3 hash key (lambda-service's canonical format)
	codeKey, err := c.s3.Store(ctx, evt.FunctionID, code)
	if err != nil {
		return fmt.Errorf("store code in lambda-service S3: %w", err)
	}

	memoryMB := evt.MemoryMB
	if memoryMB <= 0 {
		memoryMB = 128
	}
	timeoutSecs := evt.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 30
	}

	now := time.Now()
	fn := &domain.Function{
		ID:        evt.FunctionID,
		Name:      evt.Name,
		Runtime:   evt.Runtime,
		Handler:   evt.Handler,
		Code:      []byte(codeKey),
		Timeout:   time.Duration(timeoutSecs) * time.Second,
		Memory:    memoryMB,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := c.repo.Save(ctx, fn); err != nil {
		return fmt.Errorf("save function to Postgres: %w", err)
	}

	logger.Info("Function registered successfully",
		"function_id", evt.FunctionID,
		"name", evt.Name,
		"code_key", codeKey,
	)
	return nil
}

// handlerFilename derives the source file name from the handler string and runtime.
// e.g. handler="index.handler", runtime="nodejs18" → "index.js"
func handlerFilename(runtime, handler string) string {
	parts := strings.SplitN(handler, ".", 2)
	module := parts[0]
	switch {
	case strings.HasPrefix(runtime, "nodejs"):
		return module + ".js"
	case strings.HasPrefix(runtime, "python"):
		return module + ".py"
	default:
		return module
	}
}

func (c *Consumer) Close() {
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
