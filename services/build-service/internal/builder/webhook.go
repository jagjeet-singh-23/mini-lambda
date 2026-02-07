package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

// WebhookNotifier sends build status updates to webhook endpoints
type WebhookNotifier struct {
	client *http.Client
}

// NewWebhookNotifier creates a new webhook notifier
func NewWebhookNotifier() *WebhookNotifier {
	return &WebhookNotifier{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Notify sends a webhook notification
func (wn *WebhookNotifier) Notify(ctx context.Context, webhookURL, jobID, status, errorMsg string) error {
	if webhookURL == "" {
		logger.Info("No webhook URL provided, skipping notification")
		return nil
	}

	payload := WebhookPayload{
		JobID:     jobID,
		Status:    status,
		Timestamp: time.Now(),
		Error:     errorMsg,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mini-lambda-build-service")

	logger.Info("Sending webhook notification",
		"url", webhookURL,
		"job_id", jobID,
		"status", status,
	)

	resp, err := wn.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	logger.Info("Webhook notification sent successfully",
		"url", webhookURL,
		"job_id", jobID,
		"status_code", resp.StatusCode,
	)

	return nil
}

// NotifyQueued notifies that a build job has been queued
func (wn *WebhookNotifier) NotifyQueued(ctx context.Context, webhookURL, jobID string) error {
	return wn.Notify(ctx, webhookURL, jobID, string(StatusQueued), "")
}

// NotifyBuilding notifies that a build job has started
func (wn *WebhookNotifier) NotifyBuilding(ctx context.Context, webhookURL, jobID string) error {
	return wn.Notify(ctx, webhookURL, jobID, string(StatusBuilding), "")
}

// NotifyCompleted notifies that a build job has completed successfully
func (wn *WebhookNotifier) NotifyCompleted(ctx context.Context, webhookURL, jobID string) error {
	return wn.Notify(ctx, webhookURL, jobID, string(StatusCompleted), "")
}

// NotifyFailed notifies that a build job has failed
func (wn *WebhookNotifier) NotifyFailed(ctx context.Context, webhookURL, jobID, errorMsg string) error {
	return wn.Notify(ctx, webhookURL, jobID, string(StatusFailed), errorMsg)
}
