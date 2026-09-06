-- filename: 20260906130000000_managed_postgres_usage.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_usage (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    backend_id text NOT NULL,
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    window_from timestamptz NOT NULL,
    window_to timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    meter text NOT NULL CHECK (meter IN (
        'active_seconds', 'compute_unit_seconds', 'storage_byte_seconds',
        'history_byte_seconds', 'egress_bytes', 'operations'
    )),
    quantity bigint NOT NULL CHECK (quantity >= 0),
    cost_millicents bigint NOT NULL CHECK (cost_millicents >= 0),
    PRIMARY KEY (database_id, window_from, window_to, meter),
    CHECK (window_to > window_from)
);

CREATE INDEX IF NOT EXISTS managed_postgres_usage_account_period_idx
    ON managed_postgres_usage(account_id, window_from);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS managed_postgres_usage;
-- +goose StatementEnd
