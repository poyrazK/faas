-- filename: 20260926010000000_workflow_condition_checks.sql

-- +goose Up
-- +goose StatementBegin
-- The activation deadline remains in workflow_steps.started_at; this column
-- records the next scheduler wake independently of other parallel waits.
ALTER TABLE workflow_steps ADD COLUMN IF NOT EXISTS next_check_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workflow_steps DROP COLUMN IF EXISTS next_check_at;
-- +goose StatementEnd
