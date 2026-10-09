-- +goose Up
CREATE INDEX IF NOT EXISTS customer_operation_workflow_dependency_lookup
 ON customer_operation_workflow_state_reports USING gin (depends_on jsonb_path_ops)
 WHERE jsonb_array_length(depends_on)>0;

-- +goose Down
DROP INDEX customer_operation_workflow_dependency_lookup;
