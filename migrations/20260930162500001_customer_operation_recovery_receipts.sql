-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS customer_operation_recoveries (
 operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
 recovery_id text NOT NULL CHECK (octet_length(recovery_id) BETWEEN 1 AND 128),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object'),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(operation_id,recovery_id)
);
-- +goose StatementEnd

-- +goose Down
-- Forward-only: accepted recovery identities must survive application rollback.
SELECT 1;
