-- filename: 20261121140659104_run_result_contracts.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions ADD COLUMN IF NOT EXISTS result_validation jsonb;
-- +goose StatementBegin
DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_result_validation_check') THEN
ALTER TABLE executions ADD CONSTRAINT executions_result_validation_check CHECK (
  result_validation IS NULL OR (
    status = 'succeeded' AND result_validation IN (
      '{"status":"valid"}'::jsonb,
      '{"status":"invalid","message":"Run result does not match result_schema"}'::jsonb
    )
  )
) NOT VALID;
END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: older readers ignore this additive column. Retain receipts.
SELECT 1;
