-- Cron triggers table

CREATE TABLE IF NOT EXISTS cron_triggers (
    id VARCHAR(255) PRIMARY KEY,
    function_id VARCHAR(255) NOT NULL,
    cron_expression VARCHAR(100) NOT NULL,
    timezone VARCHAR(50) NOT NULL DEFAULT 'UTC',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_run_at TIMESTAMP,
    next_run_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_function
        FOREIGN KEY(function_id)
        REFERENCES functions(id)
        ON DELETE CASCADE
);

-- Index for next_run_at for efficient scheduling
CREATE INDEX IF NOT EXISTS idx_cron_triggers_function_id ON cron_triggers(function_id);
CREATE INDEX IF NOT EXISTS idx_cron_triggers_next_run ON cron_triggers(next_run_at) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_cron_triggers_enabled ON cron_triggers(enabled);


-- Webhook configurations table

CREATE TABLE IF NOT EXISTS webhooks (
    id VARCHAR(255) PRIMARY KEY,
    function_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    path VARCHAR(500) NOT NULL UNIQUE,
    secret VARCHAR(255) NOT NULL,
    signature_header VARCHAR(100) NOT NULL DEFAULT 'X-Signature',
    enabled BOOLEAN NOT NULL DEFAULT true,
    headers JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_webhooks_function_id ON webhooks(function_id);
CREATE INDEX IF NOT EXISTS idx_webhooks_path ON webhooks(path);
CREATE INDEX IF NOT EXISTS idx_webhooks_enabled ON webhooks(enabled);


-- Event subscriptions (for queue-based triggers)
CREATE TABLE IF NOT EXISTS event_subscriptions (
    id VARCHAR(255) PRIMARY KEY,
    function_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    queue_name VARCHAR(255) NOT NULL,
    routing_key VARCHAR(255),
    filter_pattern JSONB,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_event_subscriptions_function_id ON event_subscriptions(function_id);
CREATE INDEX IF NOT EXISTS idx_event_subscriptions_event_type ON event_subscriptions(event_type);
CREATE INDEX IF NOT EXISTS idx_event_subscriptions_queue_name ON event_subscriptions(queue_name);


-- Dead letter queue for failed events

CREATE TABLE IF NOT EXISTS dead_letter_queue (
    id VARCHAR(255) PRIMARY KEY,
    event_id VARCHAR(255) NOT NULL,
    function_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    payload JSONB NOT NULL,
    error_message TEXT NOT NULL,
    retry_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 3,
    first_attempt_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_attempt_at TIMESTAMP,
    next_retry_at TIMESTAMP,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_dlq_function_id ON dead_letter_queue(function_id);
CREATE INDEX IF NOT EXISTS idx_dlq_status ON dead_letter_queue(status);
CREATE INDEX IF NOT EXISTS idx_dlq_event_type ON dead_letter_queue(event_type);
CREATE INDEX IF NOT EXISTS idx_dlq_next_retry ON dead_letter_queue(next_retry_at);


-- Event audit log
CREATE TABLE IF NOT EXISTS event_audit_log (
    id VARCHAR(255) PRIMARY KEY,
    event_id VARCHAR(255) NOT NULL,
    function_id VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL,
    duration_ms BIGINT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_event_audit_function_id ON event_audit_log(function_id);
CREATE INDEX IF NOT EXISTS idx_event_audit_event_id ON event_audit_log(event_id);
CREATE INDEX IF NOT EXISTS idx_event_audit_created_at ON event_audit_log(created_at DESC);


-- Trigger for updated updated_at
create trigger update_cron_triggers_updated_at
    before update on cron_triggers
    for each row
    execute function update_updated_at_column();

create trigger update_webhooks_updated_at
    before update on webhooks
    for each row
    execute function update_updated_at_column();

create trigger update_event_subscriptions_updated_at
    before update on event_subscriptions
    for each row
    execute function update_updated_at_column();

create trigger update_dead_letter_queue_updated_at
    before update on dead_letter_queue
    for each row
    execute function update_updated_at_column();

create trigger update_event_audit_log_updated_at
    before update on event_audit_log
    for each row
    execute function update_updated_at_column();