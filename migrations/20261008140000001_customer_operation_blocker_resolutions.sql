-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports ADD COLUMN blocker_resolutions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(blocker_resolutions)='array' AND jsonb_array_length(blocker_resolutions)<=16);

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN blocker_resolutions;
