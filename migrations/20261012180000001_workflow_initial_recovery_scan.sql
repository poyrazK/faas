-- +goose Up
ALTER TABLE workflow_recovery_policies ADD COLUMN IF NOT EXISTS initial_failure_cursor uuid;

-- +goose Down
ALTER TABLE workflow_recovery_policies DROP COLUMN IF EXISTS initial_failure_cursor;
