package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	_ "github.com/lib/pq" // PostgreSQL driver
)

// PostgresConfig holds PostgreSQL connection configuration
type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string // disable, require, verify-ca, verify-full
}

// PostgresRepository implements FunctionRepository using PostgreSQL
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(config PostgresConfig) (*PostgresRepository, error) {
	// Build connection string
	// PostgreSQL DSN format: postgres://user:password@host:port/database?sslmode=disable
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host,
		config.Port,
		config.User,
		config.Password,
		config.Database,
		config.SSLMode,
	)

	// Open database connection
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	repo := &PostgresRepository{db: db}

	if err := repo.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return repo, nil
}

// Helper constructors for specific interfaces
func NewPostgresFunctionRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func NewPostgresExecutionRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func NewPostgresCronRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func NewPostgresWebhookRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func NewPostgresEventAuditRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func NewPostgresDeadLetterQueue(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// initSchema creates the database tables if they don't exist
func (r *PostgresRepository) initSchema(ctx context.Context) error {
	schema := `
	-- Functions table
	CREATE TABLE IF NOT EXISTS functions (
		id VARCHAR(255) PRIMARY KEY,
		name VARCHAR(255) NOT NULL UNIQUE,
		runtime VARCHAR(50) NOT NULL,
		handler VARCHAR(255) NOT NULL,
		code_key VARCHAR(255) NOT NULL,
		timeout_seconds INTEGER NOT NULL,
		memory_mb BIGINT NOT NULL,
		environment JSONB,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(name);
	CREATE INDEX IF NOT EXISTS idx_functions_runtime ON functions(runtime);
	CREATE INDEX IF NOT EXISTS idx_functions_created_at ON functions(created_at DESC);

	-- Cron Triggers table
	CREATE TABLE IF NOT EXISTS cron_triggers (
		id VARCHAR(255) PRIMARY KEY,
		function_id VARCHAR(255) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
		name VARCHAR(255) NOT NULL,
		cron_expression VARCHAR(255) NOT NULL,
		timezone VARCHAR(255) NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT true,
		last_run_at TIMESTAMP,
		next_run_at TIMESTAMP,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_cron_function_id ON cron_triggers(function_id);
	
	-- Event Audit Logs table
	CREATE TABLE IF NOT EXISTS event_audit_logs (
		id VARCHAR(255) PRIMARY KEY, -- using uuid
		event_id VARCHAR(255) NOT NULL,
		function_id VARCHAR(255) NOT NULL,
		event_type VARCHAR(50) NOT NULL,
		status VARCHAR(50) NOT NULL,
		duration_ms BIGINT,
		error_message TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_audit_function_id ON event_audit_logs(function_id);
	CREATE INDEX IF NOT EXISTS idx_audit_created_at ON event_audit_logs(created_at DESC);

	-- Dead Letter Queue table
	CREATE TABLE IF NOT EXISTS dead_letter_queue (
		id VARCHAR(255) PRIMARY KEY,
		event_id VARCHAR(255) NOT NULL,
		function_id VARCHAR(255) NOT NULL,
		event_type VARCHAR(50) NOT NULL,
		payload JSONB NOT NULL,
		error_message TEXT,
		retry_count INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL,
		first_attempt_at TIMESTAMP NOT NULL,
		last_attempt_at TIMESTAMP,
		next_retry_at TIMESTAMP,
		status VARCHAR(50) NOT NULL DEFAULT 'failed',
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_dlq_function_id ON dead_letter_queue(function_id);
	CREATE INDEX IF NOT EXISTS idx_dlq_status ON dead_letter_queue(status);
	
	CREATE OR REPLACE FUNCTION update_updated_at_column()
	RETURNS TRIGGER AS $$
	BEGIN
		NEW.updated_at = CURRENT_TIMESTAMP;
		RETURN NEW;
	END;
	$$ language 'plpgsql';
	
	DROP TRIGGER IF EXISTS update_functions_updated_at ON functions;
	CREATE TRIGGER update_functions_updated_at
		BEFORE UPDATE ON functions
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();

	DROP TRIGGER IF EXISTS update_cron_updated_at ON cron_triggers;
	CREATE TRIGGER update_cron_updated_at
		BEFORE UPDATE ON cron_triggers
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();

	-- Webhooks table
	CREATE TABLE IF NOT EXISTS webhooks (
		id VARCHAR(255) PRIMARY KEY,
		function_id VARCHAR(255) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
		name VARCHAR(255) NOT NULL,
		path VARCHAR(255) NOT NULL UNIQUE,
		secret VARCHAR(255) NOT NULL,
		signature_header VARCHAR(255) NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT true,
		headers JSONB,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_webhooks_function_id ON webhooks(function_id);

	DROP TRIGGER IF EXISTS update_webhooks_updated_at ON webhooks;
	CREATE TRIGGER update_webhooks_updated_at
		BEFORE UPDATE ON webhooks
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();
	`

	_, err := r.db.ExecContext(ctx, schema)
	return err
}

func (r *PostgresRepository) Save(
	ctx context.Context,
	function *domain.Function,
) error {
	var envJSON sql.NullString
	if len(function.Environment) > 0 {
		envBytes, err := json.Marshal(function.Environment)
		if err != nil {
			return fmt.Errorf("failed to marshal environment: %w", err)
		}
		envJSON = sql.NullString{String: string(envBytes), Valid: true}
	}

	query := `
	INSERT INTO functions (
		id, name, runtime, handler, code_key,
		timeout_seconds, memory_mb, environment,
		created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (id) DO UPDATE SET
		name = EXCLUDED.name,
		runtime = EXCLUDED.runtime,
		handler = EXCLUDED.handler,
		code_key = EXCLUDED.code_key,
		timeout_seconds = EXCLUDED.timeout_seconds,
		memory_mb = EXCLUDED.memory_mb,
		environment = EXCLUDED.environment,
		updated_at = CURRENT_TIMESTAMP
	`

	_, err := r.db.ExecContext(ctx, query,
		function.ID,
		function.Name,
		function.Runtime,
		function.Handler,
		string(function.Code), // This is the S3 key
		int(function.Timeout.Seconds()),
		function.Memory,
		envJSON,
		function.CreatedAt,
		function.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to save function: %w", err)
	}

	return nil
}

// FindByID retrieves a function by ID
func (r *PostgresRepository) FindByID(
	ctx context.Context,
	id string,
) (*domain.Function, error) {
	query := `
	SELECT id, name, runtime, handler, code_key,
	       timeout_seconds, memory_mb, environment,
	       created_at, updated_at
	FROM functions
	WHERE id = $1
	`

	var function domain.Function
	var timeoutSeconds int
	var envJSON []byte

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&function.ID,
		&function.Name,
		&function.Runtime,
		&function.Handler,
		&function.Code, // This will contain the S3 key
		&timeoutSeconds,
		&function.Memory,
		&envJSON,
		&function.CreatedAt,
		&function.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, domain.ErrFunctionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	// Convert timeout
	function.Timeout = time.Duration(timeoutSeconds) * time.Second

	// Deserialize environment
	if len(envJSON) > 0 {
		if err := json.Unmarshal(envJSON, &function.Environment); err != nil {
			return nil, fmt.Errorf("failed to unmarshal environment: %w", err)
		}
	}

	return &function, nil
}

// FindByName retrieves a function by name
func (r *PostgresRepository) FindByName(
	ctx context.Context,
	name string,
) (*domain.Function, error) {
	query := `
	SELECT id, name, runtime, handler, code_key,
	       timeout_seconds, memory_mb, environment,
	       created_at, updated_at
	FROM functions
	WHERE name = $1
	`

	var function domain.Function
	var timeoutSeconds int
	var envJSON []byte

	err := r.db.QueryRowContext(ctx, query, name).Scan(
		&function.ID,
		&function.Name,
		&function.Runtime,
		&function.Handler,
		&function.Code,
		&timeoutSeconds,
		&function.Memory,
		&envJSON,
		&function.CreatedAt,
		&function.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, domain.ErrFunctionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	function.Timeout = time.Duration(timeoutSeconds) * time.Second

	if len(envJSON) > 0 {
		if err := json.Unmarshal(envJSON, &function.Environment); err != nil {
			return nil, fmt.Errorf("failed to unmarshal environment: %w", err)
		}
	}

	return &function, nil
}

// List retrieves functions with pagination
func (r *PostgresRepository) List(
	ctx context.Context,
	offset, limit int,
) ([]*domain.Function, error) {
	query := `
	SELECT id, name, runtime, handler, code_key,
	       timeout_seconds, memory_mb, environment,
	       created_at, updated_at
	FROM functions
	ORDER BY created_at DESC
	LIMIT $1 OFFSET $2
	`

	// Default limit if not specified
	if limit <= 0 {
		limit = 100 // Default page size
	}

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query functions: %w", err)
	}
	defer rows.Close()

	var functions []*domain.Function

	for rows.Next() {
		var function domain.Function
		var timeoutSeconds int
		var envJSON []byte

		err := rows.Scan(
			&function.ID,
			&function.Name,
			&function.Runtime,
			&function.Handler,
			&function.Code,
			&timeoutSeconds,
			&function.Memory,
			&envJSON,
			&function.CreatedAt,
			&function.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan function: %w", err)
		}

		function.Timeout = time.Duration(timeoutSeconds) * time.Second

		if len(envJSON) > 0 {
			if err := json.Unmarshal(envJSON, &function.Environment); err != nil {
				return nil, fmt.Errorf(
					"failed to unmarshal environment: %w",
					err,
				)
			}
		}

		functions = append(functions, &function)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating functions: %w", err)
	}

	return functions, nil
}

// Delete removes a function by ID
func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(
		ctx,
		"DELETE FROM functions WHERE id = $1",
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete function: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrFunctionNotFound
	}

	return nil
}

// Count returns the total number of functions
func (r *PostgresRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM functions").
		Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count functions: %w", err)
	}
	return count, nil
}

// Exists checks if a function exists
func (r *PostgresRepository) Exists(
	ctx context.Context,
	id string,
) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM functions WHERE id = $1)", id).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check existence: %w", err)
	}
	return exists, nil
}

// Close closes the database connection
func (r *PostgresRepository) Close() error {
	return r.db.Close()
}

// Transaction support (bonus - for complex operations)
func (r *PostgresRepository) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

// SaveExecution saves an execution record to the database
func (r *PostgresRepository) SaveExecution(
	ctx context.Context,
	execution *domain.Execution,
) error {
	query := `
	INSERT INTO executions (
		id, function_id, status, is_warm_start,
		duration_ms, memory_used_bytes, error_message,
		created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	var errorMsg sql.NullString
	if execution.Error != "" {
		errorMsg = sql.NullString{String: execution.Error, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, query,
		execution.ID,
		execution.FunctionID,
		string(execution.Status),
		execution.IsWarmStart,
		execution.Duration.Milliseconds(),
		execution.MemoryUsed,
		errorMsg,
		execution.StartedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to save execution: %w", err)
	}

	return nil
}

// GetExecutionHistory retrieves execution history for a function
func (r *PostgresRepository) GetExecutionHistory(
	ctx context.Context,
	functionID string,
	limit, offset int,
) ([]*domain.Execution, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
	SELECT id, function_id, status, is_warm_start,
	       duration_ms, memory_used_bytes, error_message,
	       created_at
	FROM executions
	WHERE function_id = $1
	ORDER BY created_at DESC
	LIMIT $2 OFFSET $3
	`

	rows, err := r.db.QueryContext(ctx, query, functionID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}
	defer rows.Close()

	var executions []*domain.Execution

	for rows.Next() {
		var exec domain.Execution
		var durationMs int64
		var errorMsg sql.NullString
		var status string

		err := rows.Scan(
			&exec.ID,
			&exec.FunctionID,
			&status,
			&exec.IsWarmStart,
			&durationMs,
			&exec.MemoryUsed,
			&errorMsg,
			&exec.StartedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan execution: %w", err)
		}

		exec.Status = domain.ExecutionStatus(status)
		exec.Duration = time.Duration(durationMs) * time.Millisecond
		if errorMsg.Valid {
			exec.Error = errorMsg.String
		}

		executions = append(executions, &exec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating executions: %w", err)
	}

	return executions, nil
}

// GetRecentExecutions retrieves recent executions across all functions
func (r *PostgresRepository) GetRecentExecutions(
	ctx context.Context,
	limit int,
) ([]*domain.Execution, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
	SELECT id, function_id, status, is_warm_start,
	       duration_ms, memory_used_bytes, error_message,
	       created_at
	FROM executions
	ORDER BY created_at DESC
	LIMIT $1
	`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}
	defer rows.Close()

	var executions []*domain.Execution

	for rows.Next() {
		var exec domain.Execution
		var durationMs int64
		var errorMsg sql.NullString
		var status string

		err := rows.Scan(
			&exec.ID,
			&exec.FunctionID,
			&status,
			&exec.IsWarmStart,
			&durationMs,
			&exec.MemoryUsed,
			&errorMsg,
			&exec.StartedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan execution: %w", err)
		}

		exec.Status = domain.ExecutionStatus(status)
		exec.Duration = time.Duration(durationMs) * time.Millisecond
		if errorMsg.Valid {
			exec.Error = errorMsg.String
		}

		executions = append(executions, &exec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating executions: %w", err)
	}

	return executions, nil
}

// ExecutionStats holds aggregated execution statistics
type ExecutionStats struct {
	TotalExecutions int64
	SuccessCount    int64
	FailureCount    int64
	TimeoutCount    int64
	WarmStartCount  int64
	ColdStartCount  int64
	AvgDurationMs   float64
	SuccessRate     float64
	WarmStartRate   float64
}

// GetExecutionStats retrieves aggregated statistics for a function
func (r *PostgresRepository) GetExecutionStats(
	ctx context.Context,
	functionID string,
	since time.Time,
) (*ExecutionStats, error) {
	query := `
	SELECT
		COUNT(*) as total,
		COUNT(*) FILTER (WHERE status = 'success') as success_count,
		COUNT(*) FILTER (WHERE status = 'failed') as failure_count,
		COUNT(*) FILTER (WHERE status = 'timeout') as timeout_count,
		COUNT(*) FILTER (WHERE is_warm_start = true) as warm_start_count,
		COUNT(*) FILTER (WHERE is_warm_start = false) as cold_start_count,
		AVG(duration_ms) as avg_duration_ms
	FROM executions
	WHERE function_id = $1 AND created_at >= $2
	`

	var stats ExecutionStats
	err := r.db.QueryRowContext(ctx, query, functionID, since).Scan(
		&stats.TotalExecutions,
		&stats.SuccessCount,
		&stats.FailureCount,
		&stats.TimeoutCount,
		&stats.WarmStartCount,
		&stats.ColdStartCount,
		&stats.AvgDurationMs,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get execution stats: %w", err)
	}

	// Calculate rates
	if stats.TotalExecutions > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) /
			float64(stats.TotalExecutions) * 100
		stats.WarmStartRate = float64(stats.WarmStartCount) /
			float64(stats.TotalExecutions) * 100
	}

	return &stats, nil
}

// GetGlobalExecutionStats retrieves aggregated statistics across all functions
func (r *PostgresRepository) GetGlobalExecutionStats(
	ctx context.Context,
	since time.Time,
) (*ExecutionStats, error) {
	query := `
	SELECT
		COUNT(*) as total,
		COUNT(*) FILTER (WHERE status = 'success') as success_count,
		COUNT(*) FILTER (WHERE status = 'failed') as failure_count,
		COUNT(*) FILTER (WHERE status = 'timeout') as timeout_count,
		COUNT(*) FILTER (WHERE is_warm_start = true) as warm_start_count,
		COUNT(*) FILTER (WHERE is_warm_start = false) as cold_start_count,
		AVG(duration_ms) as avg_duration_ms
	FROM executions
	WHERE created_at >= $1
	`

	var stats ExecutionStats
	err := r.db.QueryRowContext(ctx, query, since).Scan(
		&stats.TotalExecutions,
		&stats.SuccessCount,
		&stats.FailureCount,
		&stats.TimeoutCount,
		&stats.WarmStartCount,
		&stats.ColdStartCount,
		&stats.AvgDurationMs,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get global execution stats: %w", err)
	}

	// Calculate rates
	if stats.TotalExecutions > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) /
			float64(stats.TotalExecutions) * 100
		stats.WarmStartRate = float64(stats.WarmStartCount) /
			float64(stats.TotalExecutions) * 100
	}

	return &stats, nil
}
