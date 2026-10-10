-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports ADD COLUMN change_reason jsonb NOT NULL DEFAULT 'null'::jsonb CONSTRAINT customer_operation_workflow_change_reason_check CHECK (jsonb_typeof(change_reason) IN ('object','null'));

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN change_reason;
