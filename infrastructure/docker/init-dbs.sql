-- Create separate databases for each service
CREATE DATABASE lambda_service_db;
CREATE DATABASE build_service_db;

-- Connect to lambda_service_db and create tables
\c lambda_service_db;

-- Functions table
CREATE TABLE IF NOT EXISTS functions (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    runtime VARCHAR(50) NOT NULL,
    handler VARCHAR(255) NOT NULL,
    timeout INTEGER DEFAULT 30,
    memory INTEGER DEFAULT 128,
    s3_bucket VARCHAR(255),
    s3_key VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Invocations table
CREATE TABLE IF NOT EXISTS invocations (
    id UUID PRIMARY KEY,
    function_id UUID REFERENCES functions(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    duration_ms INTEGER,
    error TEXT,
    logs TEXT
);

-- Cron jobs table
CREATE TABLE IF NOT EXISTS cron_jobs (
    id UUID PRIMARY KEY,
    function_id UUID REFERENCES functions(id) ON DELETE CASCADE,
    schedule VARCHAR(100) NOT NULL,
    payload JSONB,
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Webhooks table
CREATE TABLE IF NOT EXISTS webhooks (
    id UUID PRIMARY KEY,
    function_id UUID REFERENCES functions(id) ON DELETE CASCADE,
    url VARCHAR(500) NOT NULL,
    secret VARCHAR(255),
    events TEXT[] NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Event audit table
CREATE TABLE IF NOT EXISTS event_audit (
    id UUID PRIMARY KEY,
    event_type VARCHAR(100) NOT NULL,
    function_id UUID,
    payload JSONB,
    status VARCHAR(50) NOT NULL,
    error TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Dead letter queue table
CREATE TABLE IF NOT EXISTS dead_letter_queue (
    id UUID PRIMARY KEY,
    event_type VARCHAR(100) NOT NULL,
    function_id UUID,
    payload JSONB,
    error TEXT NOT NULL,
    retry_count INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_retry_at TIMESTAMP
);

-- Create indexes
CREATE INDEX idx_functions_name ON functions(name);
CREATE INDEX idx_invocations_function_id ON invocations(function_id);
CREATE INDEX idx_invocations_status ON invocations(status);
CREATE INDEX idx_cron_jobs_function_id ON cron_jobs(function_id);
CREATE INDEX idx_cron_jobs_enabled ON cron_jobs(enabled);
CREATE INDEX idx_event_audit_function_id ON event_audit(function_id);
CREATE INDEX idx_event_audit_created_at ON event_audit(created_at);
CREATE INDEX idx_dlq_function_id ON dead_letter_queue(function_id);

-- Connect to build_service_db and create tables
\c build_service_db;

-- Build jobs table
CREATE TABLE IF NOT EXISTS build_jobs (
    id UUID PRIMARY KEY,
    function_id UUID NOT NULL,
    runtime VARCHAR(50) NOT NULL,
    package_url VARCHAR(500) NOT NULL,
    webhook_url VARCHAR(500),
    status VARCHAR(50) NOT NULL,
    error TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP,
    completed_at TIMESTAMP
);

-- Build metadata table
CREATE TABLE IF NOT EXISTS build_metadata (
    id UUID PRIMARY KEY,
    build_job_id UUID REFERENCES build_jobs(id) ON DELETE CASCADE,
    package_size_bytes BIGINT,
    extracted_files_count INTEGER,
    build_duration_ms INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes
CREATE INDEX idx_build_jobs_function_id ON build_jobs(function_id);
CREATE INDEX idx_build_jobs_status ON build_jobs(status);
CREATE INDEX idx_build_jobs_created_at ON build_jobs(created_at);
CREATE INDEX idx_build_metadata_build_job_id ON build_metadata(build_job_id);
