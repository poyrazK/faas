-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS customer_operation_stream_leases (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS customer_operation_stream_leases_account_idx ON customer_operation_stream_leases(account_id,expires_at);
CREATE INDEX IF NOT EXISTS customer_operation_stream_leases_retention_idx ON customer_operation_stream_leases(expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
