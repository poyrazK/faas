-- filename: 20260926020000000_workflow_step_retry_deadline.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE workflow_steps ADD COLUMN IF NOT EXISTS next_retry_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workflow_steps DROP COLUMN IF EXISTS next_retry_at;
-- +goose StatementEnd
