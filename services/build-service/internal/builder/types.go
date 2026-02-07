package builder

import (
	"time"
)

// BuildJob represents a build job in the queue
type BuildJob struct {
	ID          string     `json:"id"`
	FunctionID  string     `json:"function_id"`
	Runtime     string     `json:"runtime"`
	PackageURL  string     `json:"package_url"`
	WebhookURL  string     `json:"webhook_url"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// BuildStatus represents possible build statuses
type BuildStatus string

const (
	StatusQueued    BuildStatus = "queued"
	StatusBuilding  BuildStatus = "building"
	StatusCompleted BuildStatus = "completed"
	StatusFailed    BuildStatus = "failed"
)

// CreateFunctionRequest represents the request to create a function
type CreateFunctionRequest struct {
	Name        string `json:"name"`
	Runtime     string `json:"runtime"`
	Handler     string `json:"handler"`
	PackageData []byte `json:"package_data"` // Base64 encoded ZIP
	WebhookURL  string `json:"webhook_url"`  // Client-provided webhook for status updates
	Timeout     int    `json:"timeout"`
	Memory      int    `json:"memory"`
}

// WebhookPayload represents the payload sent to webhook endpoints
type WebhookPayload struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
}
