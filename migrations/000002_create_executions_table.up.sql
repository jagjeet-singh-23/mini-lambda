CREATE TABLE IF NOT EXISTS executions (
    id VARCHAR(255) PRIMARY KEY,
    function_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,
    is_warm_start BOOLEAN NOT NULL DEFAULT false,
    duration_ms BIGINT NOT NULL,
    memory_used_bytes BIGINT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX idx_executions_function_id ON executions (function_id);
CREATE INDEX idx_executions_created_at ON executions (created_at DESC);
CREATE INDEX idx_executions_status ON executions (status);
CREATE INDEX idx_executions_warm_start ON executions (is_warm_start);
