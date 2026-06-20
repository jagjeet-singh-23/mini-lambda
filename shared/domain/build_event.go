package domain

import "time"

// FunctionBuiltQueue is the RabbitMQ queue build-service publishes to when a
// function finishes building. Lambda-service consumes it to register the
// function in its own store.
const FunctionBuiltQueue = "function.built"

// FunctionBuiltEvent is the payload published by build-service worker on
// successful zip extraction and S3 upload.
type FunctionBuiltEvent struct {
	FunctionID  string    `json:"function_id"`
	Name        string    `json:"name"`
	Runtime     string    `json:"runtime"`
	Handler     string    `json:"handler"`
	S3Prefix    string    `json:"s3_prefix"`        // e.g. "functions/{id}/"
	MemoryMB    int64     `json:"memory_mb"`
	TimeoutSecs int       `json:"timeout_seconds"`
	BuiltAt     time.Time `json:"built_at"`
}
