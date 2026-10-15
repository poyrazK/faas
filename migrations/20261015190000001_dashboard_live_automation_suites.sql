-- +goose Up
ALTER TABLE workflow_automation_live_suites ADD COLUMN managed boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE workflow_automation_live_suites DROP COLUMN managed;
