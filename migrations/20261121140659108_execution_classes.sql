-- filename: 20261121140659108_execution_classes.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions ADD COLUMN IF NOT EXISTS execution_class text NOT NULL DEFAULT 'standard';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_class_timeout_ms_check') THEN
  ALTER TABLE executions ADD CONSTRAINT executions_class_timeout_ms_check CHECK (
   timeout_ms >= 100 AND ((execution_class = 'standard' AND timeout_ms <= 30000) OR
   (execution_class = 'extended' AND timeout_ms <= 300000))
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Forward-only: retain class identity and receipts through reader rollback.
SELECT 1;
