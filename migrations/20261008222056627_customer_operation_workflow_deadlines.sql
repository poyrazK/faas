-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports
 ADD COLUMN deadline_at text NOT NULL DEFAULT '',
 ADD COLUMN deadline_only boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN deadline_only, DROP COLUMN deadline_at;
