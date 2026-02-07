package domain

import (
	"encoding/json"
	"time"
)

// EventType represents the type of event
type EventType string

const (
	EventTypeHTTP    EventType = "http"
	EventTypeCron    EventType = "cron"
	EventTypeWebhook EventType = "webhook"
	EventTypeQueue   EventType = "queue"
	EventTypeStream  EventType = "stream"
)

// Event represents a function execution trigger
type Event struct {
	ID         string            `json:"id"`
	Type       EventType         `json:"type"`
	FunctionID string            `json:"function_id"`
	Payload    json.RawMessage   `json:"payload"`
	Metadata   map[string]string `json:"metadata"`
	Timestamp  time.Time         `json:"timestamp"`
	RetryCount int               `json:"retry_count"`
	MaxRetries int               `json:"max_retries"`
}

// CronTrigger represents a scheduled function execution
type CronTrigger struct {
	ID             string    `json:"id"`
	FunctionID     string    `json:"function_id"`
	Name           string    `json:"name"`
	CronExpression string    `json:"cron_expression"` // "0 0 * * *"
	Timezone       string    `json:"timezone"`        // "Asia/Kolkata"
	Enabled        bool      `json:"enabled"`
	LastRunAt      time.Time `json:"last_run_at,omitempty"`
	NextRunAt      time.Time `json:"next_run_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Webhook represents a webhook endpoint configuration
type Webhook struct {
	ID              string            `json:"id"`
	FunctionID      string            `json:"function_id"`
	Name            string            `json:"name"`
	Path            string            `json:"path"` // "/webhooks/my-webhook"
	Secret          string            `json:"secret"`
	SignatureHeader string            `json:"signature_header"` // "X-Signature"
	Enabled         bool              `json:"enabled"`
	Headers         map[string]string `json:"headers,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// EventSubscription represents an event subscription configuration
type EventSubscription struct {
	ID            string            `json:"id"`
	FunctionID    string            `json:"function_id"`
	EventType     EventType         `json:"event_type"`
	QueueName     string            `json:"queue_name"`
	RoutingKey    string            `json:"routing_key,omitempty"`
	FilterPattern map[string]string `json:"filter_pattern,omitempty"`
	Enabled       bool              `json:"enabled"`
	CreatedAt     time.Time         `json:"created_at"`
}

// DeadLetterItem represents a failed event in the DLQ
type DeadLetterItem struct {
	ID             string          `json:"id"`
	EventID        string          `json:"event_id"`
	FunctionID     string          `json:"function_id"`
	EventType      EventType       `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	ErrorMessage   string          `json:"error_message"`
	RetryCount     int             `json:"retry_count"`
	MaxRetries     int             `json:"max_retries"`
	FirstAttemptAt time.Time       `json:"first_attempt_at"`
	LastAttemptAt  time.Time       `json:"last_attempt_at,omitempty"`
	NextRetryAt    time.Time       `json:"next_retry_at,omitempty"`
	Status         string          `json:"status"` // pending, retrying, failed
}

// EventAuditLog represents an audit log for event processing
type EventAuditLog struct {
	ID           string    `json:"id"`
	EventID      string    `json:"event_id"`
	FunctionID   string    `json:"function_id"`
	EventType    EventType `json:"event_type"`
	Status       string    `json:"status"` // pending, processing, completed, failed
	DurationMs   int64     `json:"duration_ms,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Validate methods
func (c *CronTrigger) Validate() error {
	if c.FunctionID == "" {
		return NewDomainError("function_id is required")
	}
	if c.CronExpression == "" {
		return NewDomainError("cron_expression is required")
	}
	if c.Name == "" {
		return NewDomainError("name is required")
	}
	return nil
}

func (w *Webhook) Validate() error {
	if w.FunctionID == "" {
		return NewDomainError("function_id is required")
	}
	if w.Name == "" {
		return NewDomainError("name is required")
	}
	if w.Path == "" {
		return NewDomainError("path is required")
	}
	if w.Secret == "" {
		return NewDomainError("secret is required")
	}
	if w.SignatureHeader == "" {
		return NewDomainError("signature_header is required")
	}
	return nil
}

func (e *EventSubscription) Validate() error {
	if e.FunctionID == "" {
		return NewDomainError("function_id is required")
	}
	if e.EventType == "" {
		return NewDomainError("event_type is required")
	}
	if e.QueueName == "" {
		return NewDomainError("queue_name is required")
	}
	return nil
}
