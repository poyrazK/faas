-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports
 ADD COLUMN blockers jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(blockers)='array' AND jsonb_array_length(blockers)<=16),
 ADD COLUMN blockers_only boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN blockers_only, DROP COLUMN blockers;
