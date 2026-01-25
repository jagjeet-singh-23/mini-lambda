package events

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
)

// WebhookRepository defines the interface for webhook storage operations
type WebhookRepository interface {
	SaveWebhook(ctx context.Context, webhook *domain.Webhook) error
	GetWebhookByPath(ctx context.Context, path string) (*domain.Webhook, error)
	DeleteWebhook(ctx context.Context, id string) error
}

// WebhookHandler handles webhook-related HTTP requests
type WebhookHandler struct {
	repository WebhookRepository
	eventBus   EventBus
}

func NewWebhookHandler(
	repository WebhookRepository,
	eventBus EventBus,
) *WebhookHandler {
	return &WebhookHandler{
		repository: repository,
		eventBus:   eventBus,
	}
}

// RegisterWebhook creates a new webhook endpoint
func (h *WebhookHandler) RegisterWebhook(ctx context.Context, webhook *domain.Webhook) error {
	if err := webhook.Validate(); err != nil {
		return err
	}

	if webhook.ID == "" {
		webhook.ID = uuid.Must(uuid.NewV4()).String()
	}

	webhook.CreatedAt = time.Now()
	webhook.UpdatedAt = time.Now()

	if webhook.SignatureHeader == "" {
		webhook.SignatureHeader = "X-Signature"
	}

	return h.repository.SaveWebhook(ctx, webhook)
}

// HandleRequest handles incoming webhook requests
func (h *WebhookHandler) HandleRequest(
	ctx context.Context,
	path string,
	payload []byte,
	headers map[string]string,
) error {
	webhook, err := h.repository.GetWebhookByPath(ctx, path)
	if err != nil {
		return err
	}

	if webhook == nil {
		return fmt.Errorf("webhook not found")
	}

	if signature, exists := headers[webhook.SignatureHeader]; exists {
		if !h.ValidateSignature(webhook, payload, signature) {
			return fmt.Errorf("invalid signature")
		}
	}

	event := &domain.Event{
		ID:         uuid.Must(uuid.NewV4()).String(),
		Type:       domain.EventTypeWebhook,
		FunctionID: webhook.FunctionID,
		Payload:    json.RawMessage(payload),
		Metadata: map[string]string{
			"webhook_id":   webhook.ID,
			"webhook_name": webhook.Name,
			"webhook_path": webhook.Path,
		},
		Timestamp:  time.Now(),
		RetryCount: 0,
		MaxRetries: 3,
	}

	for k, v := range webhook.Headers {
		event.Metadata["header_"+k] = v
	}

	if err := h.eventBus.Publish(ctx, event); err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	return nil
}

// ValidateSignature validates the webhook signature
func (h *WebhookHandler) ValidateSignature(
	webhook *domain.Webhook,
	payload []byte,
	signature string,
) bool {
	signature = removePrefix(signature, "sha256=")
	signature = removePrefix(signature, "sha1=")
	signature = removePrefix(signature, "v0=")

	mac := hmac.New(sha256.New, []byte(webhook.Secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

func removePrefix(s, prefix string) string {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// ValidateGithubSignature validates the github webhook signature
func ValidateGithubSignature(secret string, payload []byte, signature string) bool {
	if len(signature) < 7 || signature[:7] != "sha256=" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// ValidateStripeSignature validates the stripe webhook signature
func ValidateStripeSignature(secret string, payload []byte, signature string, timestamp string) bool {
	signedPayload := fmt.Sprintf("%s.%s", timestamp, string(payload))

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}
