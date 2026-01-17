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

CREATE INDEX idx_functions_name ON functions (name);

CREATE INDEX idx_functions_runtime ON functions (runtime);

CREATE INDEX idx_functions_created_at ON functions (created_at DESC);


CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_functions_updated_at
BEFORE UPDATE ON functions
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();