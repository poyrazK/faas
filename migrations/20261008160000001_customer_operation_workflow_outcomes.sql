-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports
 ADD COLUMN outcome_code text NOT NULL DEFAULT '',
 ADD COLUMN outcome_description text NOT NULL DEFAULT '',
 ADD COLUMN outcome_only boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports
 DROP COLUMN outcome_only, DROP COLUMN outcome_description, DROP COLUMN outcome_code;
