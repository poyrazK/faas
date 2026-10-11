-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports ADD COLUMN IF NOT EXISTS selected_paths jsonb NOT NULL DEFAULT '[]'::jsonb CONSTRAINT customer_operation_workflow_selected_paths_check CHECK (jsonb_typeof(selected_paths)='array' AND jsonb_array_length(selected_paths)<=8);

-- +goose Down
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN IF EXISTS selected_paths;
