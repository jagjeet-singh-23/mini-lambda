package events

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/robfig/cron/v3"
)

type CronScheduler struct {
	cron       *cron.Cron
	eventBus   EventBus
	repository CronRepository
	entries    map[string]cron.EntryID // triggerID -> CronEntryID
}

type CronRepository interface {
	SaveTrigger(ctx context.Context, trigger *domain.CronTrigger) error
	GetTrigger(ctx context.Context, triggerID string) (*domain.CronTrigger, error)
	ListTriggers(
		ctx context.Context,
		functionID string,
	) ([]*domain.CronTrigger, error)
	DeleteTrigger(ctx context.Context, triggerID string) error
	ListAllEnabled(ctx context.Context) ([]*domain.CronTrigger, error)
	UpdateNextRun(
		ctx context.Context,
		triggerID string,
		nextRunAt time.Time,
	) error
}

// NewCronScheduler creates a new cron scheduler
func NewCronScheduler(
	c *cron.Cron,
	eventBus EventBus,
	repository CronRepository,
) *CronScheduler {
	return &CronScheduler{
		cron:       c,
		eventBus:   eventBus,
		repository: repository,
		entries:    make(map[string]cron.EntryID),
	}
}

func (s *CronScheduler) AddTrigger(
	ctx context.Context,
	trigger *domain.CronTrigger,
) error {
	if err := trigger.Validate(); err != nil {
		return err
	}

	if trigger.ID == "" {
		triggerID, err := uuid.NewV4()
		if err != nil {
			return fmt.Errorf("failed to generate trigger ID: %w", err)
		}
		trigger.ID = triggerID.String()
	}

	schedule, err := s.parseSchedule(trigger.CronExpression, trigger.Timezone)
	if err != nil {
		return fmt.Errorf("failed to parse schedule: %w", err)
	}

	now := time.Now()
	trigger.NextRunAt = schedule.Next(now)
	trigger.CreatedAt = now
	trigger.UpdatedAt = now

	if err := s.repository.SaveTrigger(ctx, trigger); err != nil {
		return fmt.Errorf("failed to save trigger: %w", err)
	}

	if !trigger.Enabled {
		return nil
	}

	entryID, err := s.cron.AddFunc(trigger.CronExpression, s.createJobFunc(trigger))

	if err != nil {
		return fmt.Errorf("failed to add cron entry: %w", err)
	}

	s.entries[trigger.ID] = entryID
	return nil
}

func (s *CronScheduler) RemoveTrigger(ctx context.Context, triggerID string) error {
	if entryID, exists := s.entries[triggerID]; exists {
		s.cron.Remove(entryID)
		delete(s.entries, triggerID)
	}

	// Delete from DB
	if err := s.repository.DeleteTrigger(ctx, triggerID); err != nil {
		return fmt.Errorf("failed to delete trigger: %w", err)
	}

	log.Println("Cron trigger removed successfully: ", triggerID)
	return nil
}

// Start begins the scheduler
func (s *CronScheduler) Start(ctx context.Context) error {
	// Load existing triggers from DB
	if err := s.loadTriggers(ctx); err != nil {
		return fmt.Errorf("failed to load triggers: %w", err)
	}

	s.cron.Start()
	log.Println("Cron scheduler started...")

	<-ctx.Done()
	return s.Stop(context.Background())
}

// Stop halts the scheduler
func (s *CronScheduler) Stop(ctx context.Context) error {
	log.Println("Stopping Cron scheduler...")
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	log.Println("Cron scheduler stopped...")
	return nil
}

// loadTriggers loads all enabled triggers from DB
func (s *CronScheduler) loadTriggers(ctx context.Context) error {
	triggers, err := s.repository.ListTriggers(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to list triggers: %w", err)
	}

	for _, trigger := range triggers {
		if !trigger.Enabled {
			continue
		}

		if err := s.AddTrigger(ctx, trigger); err != nil {
			log.Printf("Failed to add trigger: %v", err)
		}
	}
	return nil
}

// createJobFunc creates a job function for a trigger
func (s *CronScheduler) createJobFunc(trigger *domain.CronTrigger) func() {
	return func() {
		ctx := context.Background()

		event := &domain.Event{
			ID:         uuid.Must(uuid.NewV4()).String(),
			FunctionID: trigger.FunctionID,
			Type:       domain.EventTypeCron,
			Payload:    s.buildCronPayload(trigger),
			Metadata: map[string]string{
				"trigger_id":   trigger.ID,
				"trigger_name": trigger.Name,
				"schedule":     trigger.CronExpression,
			},
			Timestamp:  time.Now(),
			RetryCount: 0,
			MaxRetries: 3,
		}

		// Publish event to event bus
		if err := s.eventBus.Publish(ctx, event); err != nil {
			log.Printf("Failed to publish cron event: %v", err)
			return
		}

		now := time.Now()
		trigger.LastRunAt = now

		schedule, _ := s.parseSchedule(trigger.CronExpression, trigger.Timezone)
		trigger.NextRunAt = schedule.Next(now)

		s.repository.UpdateNextRun(ctx, trigger.ID, trigger.NextRunAt)

		log.Printf("Cron job executed successfully: %s", trigger.ID)
	}
}
