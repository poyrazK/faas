-- +goose Up
CREATE INDEX customer_operation_workflow_states_attention_idx ON customer_operation_workflow_states(account_id,app_id,scope,updated_at DESC);

-- +goose Down
DROP INDEX customer_operation_workflow_states_attention_idx;
