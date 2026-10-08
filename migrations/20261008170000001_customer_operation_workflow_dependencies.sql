-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports
 ADD COLUMN depends_on jsonb NOT NULL DEFAULT '[]'::jsonb
  CHECK (jsonb_typeof(depends_on)='array' AND jsonb_array_length(depends_on)<=16),
 ADD COLUMN dependencies_only boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN dependencies_only, DROP COLUMN depends_on;
