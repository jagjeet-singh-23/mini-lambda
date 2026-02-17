package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
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

	if err := wn.validateWebhookURL(webhookURL); err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
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

// validateWebhookURL checks if the webhook URL is safe (SSRF protection)
func (wn *WebhookNotifier) validateWebhookURL(rawURL string) error {
	// Check if private webhooks are allowed
	if os.Getenv("ALLOW_PRIVATE_WEBHOOKS") == "true" {
		return nil
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("invalid scheme: %s", parsedURL.Scheme)
	}

	hostname := parsedURL.Hostname()
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("webhook URL resolves to private IP: %s", ip)
		}
	}

	return nil
}

// isPrivateIP checks if an IP is in a private block
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast()
}
