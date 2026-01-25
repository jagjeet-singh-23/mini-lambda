package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
)

func (r *PostgresRepository) SaveTrigger(ctx context.Context, trigger *domain.CronTrigger) error {
	query := `
	INSERT INTO cron_triggers (
		id, function_id, name, cron_expression, timezone,
		enabled, last_run_at, next_run_at, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (id) DO UPDATE SET
		function_id = EXCLUDED.function_id,
		name = EXCLUDED.name,
		cron_expression = EXCLUDED.cron_expression,
		timezone = EXCLUDED.timezone,
		enabled = EXCLUDED.enabled,
		last_run_at = EXCLUDED.last_run_at,
		next_run_at = EXCLUDED.next_run_at,
		updated_at = CURRENT_TIMESTAMP
	`
	// Handle nil time for LastRunAt if it's zero
	var lastRunAt interface{}
	if trigger.LastRunAt.IsZero() {
		lastRunAt = nil
	} else {
		lastRunAt = trigger.LastRunAt
	}

	_, err := r.db.ExecContext(ctx, query,
		trigger.ID,
		trigger.FunctionID,
		trigger.Name,
		trigger.CronExpression,
		trigger.Timezone,
		trigger.Enabled,
		lastRunAt,
		trigger.NextRunAt,
		trigger.CreatedAt,
		trigger.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save cron trigger: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetTrigger(ctx context.Context, triggerID string) (*domain.CronTrigger, error) {
	query := `
	SELECT id, function_id, name, cron_expression, timezone,
	       enabled, last_run_at, next_run_at, created_at, updated_at
	FROM cron_triggers
	WHERE id = $1
	`
	var t domain.CronTrigger
	var lastRunAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, triggerID).Scan(
		&t.ID,
		&t.FunctionID,
		&t.Name,
		&t.CronExpression,
		&t.Timezone,
		&t.Enabled,
		&lastRunAt,
		&t.NextRunAt,
		&t.CreatedAt,
		&t.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("cron trigger not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get cron trigger: %w", err)
	}

	if lastRunAt.Valid {
		t.LastRunAt = lastRunAt.Time
	}

	return &t, nil
}

func (r *PostgresRepository) ListTriggers(
	ctx context.Context,
	functionID string,
) ([]*domain.CronTrigger, error) {
	query := `
	SELECT id, function_id, name, cron_expression, timezone,
	       enabled, last_run_at, next_run_at, created_at, updated_at
	FROM cron_triggers
	`
	var args []interface{}
	if functionID != "" {
		query += " WHERE function_id = $1"
		args = append(args, functionID)
	}
	query += " ORDER BY created_at DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list cron triggers: %w", err)
	}
	defer rows.Close()

	var triggers []*domain.CronTrigger
	for rows.Next() {
		var t domain.CronTrigger
		var lastRunAt sql.NullTime
		err := rows.Scan(
			&t.ID,
			&t.FunctionID,
			&t.Name,
			&t.CronExpression,
			&t.Timezone,
			&t.Enabled,
			&lastRunAt,
			&t.NextRunAt,
			&t.CreatedAt,
			&t.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan cron trigger: %w", err)
		}
		if lastRunAt.Valid {
			t.LastRunAt = lastRunAt.Time
		}
		triggers = append(triggers, &t)
	}
	return triggers, nil
}

func (r *PostgresRepository) ListAllEnabled(ctx context.Context) ([]*domain.CronTrigger, error) {
	query := `
	SELECT id, function_id, name, cron_expression, timezone,
	       enabled, last_run_at, next_run_at, created_at, updated_at
	FROM cron_triggers
	WHERE enabled = true
	ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list cron triggers: %w", err)
	}
	defer rows.Close()

	var triggers []*domain.CronTrigger
	for rows.Next() {
		var t domain.CronTrigger
		var lastRunAt sql.NullTime
		err := rows.Scan(
			&t.ID,
			&t.FunctionID,
			&t.Name,
			&t.CronExpression,
			&t.Timezone,
			&t.Enabled,
			&lastRunAt,
			&t.NextRunAt,
			&t.CreatedAt,
			&t.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan cron trigger: %w", err)
		}
		if lastRunAt.Valid {
			t.LastRunAt = lastRunAt.Time
		}
		triggers = append(triggers, &t)
	}
	return triggers, nil
}

func (r *PostgresRepository) DeleteTrigger(ctx context.Context, triggerID string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM cron_triggers WHERE id = $1", triggerID)
	if err != nil {
		return fmt.Errorf("failed to delete cron trigger: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("cron trigger not found")
	}
	return nil
}

func (r *PostgresRepository) UpdateNextRun(
	ctx context.Context,
	triggerID string,
	nextRunAt time.Time,
) error {
	query := `
	UPDATE cron_triggers
	SET next_run_at = $1, last_run_at = CURRENT_TIMESTAMP
	WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, nextRunAt, triggerID)
	if err != nil {
		return fmt.Errorf("failed to update next run: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------
// EventAuditRepository Implementation
// ---------------------------------------------------------------------

func (r *PostgresRepository) Add(ctx context.Context, auditLog *domain.EventAuditLog) error {
	// Generate ID if missing
	if auditLog.ID == "" {
		newID, err := uuid.NewV4()
		if err != nil {
			return fmt.Errorf("failed to generate audit log ID: %w", err)
		}
		auditLog.ID = newID.String()
	}

	query := `
	INSERT INTO event_audit_logs (
		id, event_id, function_id, event_type, status,
		duration_ms, error_message, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.db.ExecContext(ctx, query,
		auditLog.ID,
		auditLog.EventID,
		auditLog.FunctionID,
		string(auditLog.EventType),
		auditLog.Status,
		auditLog.DurationMs,
		auditLog.ErrorMessage,
		auditLog.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}
	return nil
}

func (r *PostgresRepository) Get(
	ctx context.Context,
	functionID string,
	limit, offset int,
) ([]*domain.EventAuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
	SELECT id, event_id, function_id, event_type, status,
	       duration_ms, error_message, created_at
	FROM event_audit_logs
	WHERE function_id = $1
	ORDER BY created_at DESC
	LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, query, functionID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*domain.EventAuditLog
	for rows.Next() {
		var l domain.EventAuditLog
		var errorMsg sql.NullString
		var eventType string

		err := rows.Scan(
			&l.ID,
			&l.EventID,
			&l.FunctionID,
			&eventType,
			&l.Status,
			&l.DurationMs,
			&errorMsg,
			&l.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}
		l.EventType = domain.EventType(eventType)
		if errorMsg.Valid {
			l.ErrorMessage = errorMsg.String
		}
		logs = append(logs, &l)
	}
	return logs, nil
}

// ---------------------------------------------------------------------
// DeadLetterQueue Implementation
// ---------------------------------------------------------------------

func (r *PostgresRepository) Push(ctx context.Context, item *domain.DeadLetterItem) error {
	// Generate ID if missing
	if item.ID == "" {
		newID, err := uuid.NewV4()
		if err != nil {
			return fmt.Errorf("failed to generate DLQ item ID: %w", err)
		}
		item.ID = newID.String()
	}

	query := `
	INSERT INTO dead_letter_queue (
		id, event_id, function_id, event_type, payload,
		error_message, retry_count, max_retries,
		first_attempt_at, last_attempt_at, next_retry_at, status, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, CURRENT_TIMESTAMP)
	ON CONFLICT (id) DO UPDATE SET
		retry_count = EXCLUDED.retry_count,
		last_attempt_at = EXCLUDED.last_attempt_at,
		next_retry_at = EXCLUDED.next_retry_at,
		status = EXCLUDED.status,
		error_message = EXCLUDED.error_message
	`

	// Prepare nullable fields
	var lastAttemptAt any = nil
	if !item.LastAttemptAt.IsZero() {
		lastAttemptAt = item.LastAttemptAt
	}
	var nextRetryAt any = nil
	if !item.NextRetryAt.IsZero() {
		nextRetryAt = item.NextRetryAt
	}

	payloadBytes, err := json.Marshal(item.Payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query,
		item.ID,
		item.EventID,
		item.FunctionID,
		string(item.EventType),
		payloadBytes,
		item.ErrorMessage,
		item.RetryCount,
		item.MaxRetries,
		item.FirstAttemptAt,
		lastAttemptAt,
		nextRetryAt,
		item.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to push to DLQ: %w", err)
	}
	return nil
}

func (q *PostgresRepository) Retry(
	ctx context.Context,
	itemID string,
) error {
	// Update status to retrying
	query := `
		UPDATE dead_letter_queue 
		SET status = 'retrying', 
		    last_attempt_at = NOW(),
		    retry_count = retry_count + 1
		WHERE id = $1
		RETURNING
			event_id,
			function_id,
			event_type,
			payload,
			retry_count,
			max_retries
	`

	var eventID, functionID string
	var eventType domain.EventType
	var payloadJSON []byte
	var retryCount, maxRetries int

	err := q.db.QueryRowContext(ctx, query, itemID).Scan(
		&eventID, &functionID, &eventType, &payloadJSON, &retryCount, &maxRetries,
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *PostgresRepository) ListDeadLetterItems(
	ctx context.Context,
	limit, offset int,
) ([]*domain.DeadLetterItem, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
	SELECT id, event_id, function_id, event_type, payload,
	       error_message, retry_count, max_retries,
	       first_attempt_at, last_attempt_at, next_retry_at, status
	FROM dead_letter_queue
	ORDER BY created_at DESC
	LIMIT $1 OFFSET $2
	`
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list DLQ items: %w", err)
	}
	defer rows.Close()

	var items []*domain.DeadLetterItem
	for rows.Next() {
		var item domain.DeadLetterItem
		var payloadBytes []byte
		var eventType string
		var lastAttemptAt sql.NullTime
		var nextRetryAt sql.NullTime

		err := rows.Scan(
			&item.ID,
			&item.EventID,
			&item.FunctionID,
			&eventType,
			&payloadBytes,
			&item.ErrorMessage,
			&item.RetryCount,
			&item.MaxRetries,
			&item.FirstAttemptAt,
			&lastAttemptAt,
			&nextRetryAt,
			&item.Status,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan DLQ item: %w", err)
		}

		item.EventType = domain.EventType(eventType)
		if err := json.Unmarshal(payloadBytes, &item.Payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
		if lastAttemptAt.Valid {
			item.LastAttemptAt = lastAttemptAt.Time
		}
		if nextRetryAt.Valid {
			item.NextRetryAt = nextRetryAt.Time
		}

		items = append(items, &item)
	}
	return items, nil
}

func (r *PostgresRepository) DeleteDeadLetterItem(ctx context.Context, itemID string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM dead_letter_queue WHERE id = $1", itemID)
	if err != nil {
		return fmt.Errorf("failed to delete DLQ item: %w", err)
	}
	return nil
}

func (r *PostgresRepository) SaveWebhook(ctx context.Context, webhook *domain.Webhook) error {
	headersBytes, err := json.Marshal(webhook.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}

	query := `
	INSERT INTO webhooks (
		id, function_id, name, path, secret, signature_header,
		enabled, headers, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (id) DO UPDATE SET
		name = EXCLUDED.name,
		path = EXCLUDED.path,
		secret = EXCLUDED.secret,
		signature_header = EXCLUDED.signature_header,
		enabled = EXCLUDED.enabled,
		headers = EXCLUDED.headers,
		updated_at = CURRENT_TIMESTAMP
	`

	_, err = r.db.ExecContext(ctx, query,
		webhook.ID,
		webhook.FunctionID,
		webhook.Name,
		webhook.Path,
		webhook.Secret,
		webhook.SignatureHeader,
		webhook.Enabled,
		headersBytes,
		webhook.CreatedAt,
		webhook.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to save webhook: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetWebhookByPath(ctx context.Context, path string) (*domain.Webhook, error) {
	query := `
	SELECT id, function_id, name, path, secret, signature_header,
	       enabled, headers, created_at, updated_at
	FROM webhooks
	WHERE path = $1
	`

	var w domain.Webhook
	var headersBytes []byte

	err := r.db.QueryRowContext(ctx, query, path).Scan(
		&w.ID,
		&w.FunctionID,
		&w.Name,
		&w.Path,
		&w.Secret,
		&w.SignatureHeader,
		&w.Enabled,
		&headersBytes,
		&w.CreatedAt,
		&w.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get webhook: %w", err)
	}

	if len(headersBytes) > 0 {
		if err := json.Unmarshal(headersBytes, &w.Headers); err != nil {
			return nil, fmt.Errorf("failed to unmarshal headers: %w", err)
		}
	}

	return &w, nil
}

func (r *PostgresRepository) DeleteWebhook(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM webhooks WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("webhook not found")
	}
	return nil
}
