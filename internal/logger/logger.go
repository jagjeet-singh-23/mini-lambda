package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gofrs/uuid/v5"
)

// LogLevel represents the severity of a log entry
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// ContextKey is the type for context keys
type ContextKey string

const (
	// CorrelationIDKey is the context key for correlation IDs
	CorrelationIDKey ContextKey = "correlation_id"
	// RequestIDKey is the context key for request IDs
	RequestIDKey ContextKey = "request_id"
)

// Logger provides structured logging with JSON output
type Logger struct {
	output io.Writer
	level  LogLevel
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp     string                 `json:"timestamp"`
	Level         string                 `json:"level"`
	Message       string                 `json:"message"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	RequestID     string                 `json:"request_id,omitempty"`
	Fields        map[string]interface{} `json:"fields,omitempty"`
}

// NewLogger creates a new structured logger
func NewLogger(output io.Writer, level LogLevel) *Logger {
	if output == nil {
		output = os.Stdout
	}
	return &Logger{
		output: output,
		level:  level,
	}
}

// DefaultLogger returns a logger with default settings
func DefaultLogger() *Logger {
	return NewLogger(os.Stdout, LevelInfo)
}

// WithContext creates a logger with context values
func (l *Logger) WithContext(ctx context.Context) *ContextLogger {
	return &ContextLogger{
		logger: l,
		ctx:    ctx,
	}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...map[string]interface{}) {
	l.log(LevelDebug, msg, nil, fields...)
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...map[string]interface{}) {
	l.log(LevelInfo, msg, nil, fields...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...map[string]interface{}) {
	l.log(LevelWarn, msg, nil, fields...)
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...map[string]interface{}) {
	l.log(LevelError, msg, nil, fields...)
}

func (l *Logger) log(
	level LogLevel,
	msg string,
	ctx context.Context,
	fields ...map[string]interface{},
) {
	if !l.shouldLog(level) {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     string(level),
		Message:   msg,
	}

	// Extract context values if available
	if ctx != nil {
		if corrID, ok := ctx.Value(CorrelationIDKey).(string); ok {
			entry.CorrelationID = corrID
		}
		if reqID, ok := ctx.Value(RequestIDKey).(string); ok {
			entry.RequestID = reqID
		}
	}

	// Merge all field maps
	if len(fields) > 0 {
		entry.Fields = make(map[string]interface{})
		for _, fieldMap := range fields {
			for k, v := range fieldMap {
				entry.Fields[k] = v
			}
		}
	}

	// Write JSON to output
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal log entry: %v\n", err)
		return
	}

	fmt.Fprintln(l.output, string(data))
}

func (l *Logger) shouldLog(level LogLevel) bool {
	levels := map[LogLevel]int{
		LevelDebug: 0,
		LevelInfo:  1,
		LevelWarn:  2,
		LevelError: 3,
	}
	return levels[level] >= levels[l.level]
}

// ContextLogger wraps a logger with context
type ContextLogger struct {
	logger *Logger
	ctx    context.Context
}

// Debug logs a debug message with context
func (cl *ContextLogger) Debug(msg string, fields ...map[string]interface{}) {
	cl.logger.log(LevelDebug, msg, cl.ctx, fields...)
}

// Info logs an info message with context
func (cl *ContextLogger) Info(msg string, fields ...map[string]interface{}) {
	cl.logger.log(LevelInfo, msg, cl.ctx, fields...)
}

// Warn logs a warning message with context
func (cl *ContextLogger) Warn(msg string, fields ...map[string]interface{}) {
	cl.logger.log(LevelWarn, msg, cl.ctx, fields...)
}

// Error logs an error message with context
func (cl *ContextLogger) Error(msg string, fields ...map[string]interface{}) {
	cl.logger.log(LevelError, msg, cl.ctx, fields...)
}

// GenerateCorrelationID generates a new correlation ID
func GenerateCorrelationID() string {
	id, _ := uuid.NewV4()
	return id.String()
}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, CorrelationIDKey, correlationID)
}

// WithRequestID adds a request ID to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// GetCorrelationID retrieves the correlation ID from context
func GetCorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(CorrelationIDKey).(string); ok {
		return id
	}
	return ""
}

// GetRequestID retrieves the request ID from context
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}
