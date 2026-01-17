DROP TRIGGER IF EXISTS update_functions_updated_at ON functions;

DROP FUNCTION IF EXISTS update_updated_at_column();

DROP INDEX IF EXISTS idx_functions_runtime;
DROP INDEX IF EXISTS idx_functions_name;

DROP TABLE IF EXISTS functions;