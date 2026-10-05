-- filename: 20261005110055936_customer_operation_operator_history_index.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-521: bounded account operator history must not scan every tenant.
CREATE INDEX IF NOT EXISTS customer_operations_account_app_creation_idx
    ON customer_operations(account_id, app_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS customer_operations_account_app_creation_idx;
