package events

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
)

// EventProcessor interface defines the contract for processing events
type EventProcessor interface {
	Process(ctx context.Context, event *domain.Event) error
}

type EventBus interface {
	Publish(ctx context.Context, event *domain.Event) error
	Subscribe(ctx context.Context, eventType domain.EventType, functionID string) error
	Unsubscribe(eventType domain.EventType, functionID string) error
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

type DefaultEventProcessor struct {
	functionService *domain.FunctionService
	runtimeManager  domain.RuntimeManager
	auditRepo       EventAuditRepository
	dlq             DeadLetterQueue
}

func NewDefaultEventProcessor(
	functionService *domain.FunctionService,
	runtimeManager domain.RuntimeManager,
	auditRepo EventAuditRepository,
	dlq DeadLetterQueue,
) *DefaultEventProcessor {
	return &DefaultEventProcessor{
		functionService: functionService,
		runtimeManager:  runtimeManager,
		auditRepo:       auditRepo,
		dlq:             dlq,
	}
}

func (p *DefaultEventProcessor) Process(
	ctx context.Context,
	event *domain.Event,
) error {
	startTime := time.Now()

	// Get function
	function, err := p.functionService.GetFunction(ctx, event.FunctionID)
	if err != nil {
		return p.handleError(ctx, event, err, startTime)
	}

	execCtx, cancel := context.WithTimeout(ctx, function.Timeout)
	defer cancel()

	result, err := p.runtimeManager.Execute(execCtx, function, event.Payload)
	duration := time.Since(startTime)

	if err != nil {
		return p.handleError(ctx, event, err, startTime)
	}

	p.logAudit(ctx, &domain.EventAuditLog{
		EventID:    event.ID,
		FunctionID: event.FunctionID,
		EventType:  event.Type,
		Status:     "success",
		DurationMs: duration.Milliseconds(),
		CreatedAt:  time.Now(),
	})

	log.Printf("Event processed successfully: %s", event.ID)

	// Save execution record
	execution := domain.NewExecution(function.ID, event.Payload)
	execution.MarkStarted()
	execution.MarkSuccess(result.Output)
	execution.MemoryUsed = result.MemoryUsed
	execution.IsWarmStart = result.WasWarmStart
	p.functionService.SaveExecution(ctx, execution)

	return nil
}

// handleError processes execution errors
func (p *DefaultEventProcessor) handleError(
	ctx context.Context,
	event *domain.Event,
	err error,
	startTime time.Time,
) error {
	duration := time.Since(startTime)
	p.logAudit(ctx, &domain.EventAuditLog{
		EventID:    event.ID,
		FunctionID: event.FunctionID,
		EventType:  event.Type,
		Status:     "failed",
		DurationMs: duration.Milliseconds(),
		CreatedAt:  time.Now(),
	})

	if event.RetryCount >= event.MaxRetries {
		dlqItem := &domain.DeadLetterItem{
			EventID:        event.ID,
			FunctionID:     event.FunctionID,
			EventType:      event.Type,
			Payload:        event.Payload,
			ErrorMessage:   err.Error(),
			RetryCount:     event.RetryCount,
			MaxRetries:     event.MaxRetries,
			FirstAttemptAt: time.Now(),
			Status:         "failed",
		}
		if dlqErr := p.dlq.Push(ctx, dlqItem); dlqErr != nil {
			return fmt.Errorf("failed to add event to DLQ: %w", dlqErr)
		}
		log.Printf("Event added to DLQ: %s", event.ID)
		return nil
	}

	return nil
}

// logAudit logs event audit information
func (p *DefaultEventProcessor) logAudit(
	ctx context.Context,
	auditLog *domain.EventAuditLog,
) {
	if p.auditRepo == nil {
		return
	}

	if err := p.auditRepo.Add(ctx, auditLog); err != nil {
		// Don't fail the execution if audit logging fails
		log.Printf("Failed to log audit: %v", err)
	}
}

// EventAuditRepository interface defines the contract for event audit repository
type EventAuditRepository interface {
	Add(ctx context.Context, auditLog *domain.EventAuditLog) error
	Get(
		ctx context.Context,
		functionID string,
		limit, offset int,
	) ([]*domain.EventAuditLog, error)
}

// DeadLetterQueue interface defines the contract for dead letter queue
type DeadLetterQueue interface {
	Push(ctx context.Context, item *domain.DeadLetterItem) error
	Retry(ctx context.Context, itemID string) error
	ListDeadLetterItems(
		ctx context.Context,
		limit, offset int,
	) ([]*domain.DeadLetterItem, error)
	DeleteDeadLetterItem(ctx context.Context, itemID string) error
}
