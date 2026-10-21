-- filename: 20261017082356665_managed_postgres_connection_capacity.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_connection_capacity (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    backend_id text NOT NULL CHECK (backend_id <> ''),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[0-9a-f]{64}$'),
    provider_resource_id text NOT NULL CHECK (provider_resource_id <> ''),
    data_resource_id text NOT NULL CHECK (data_resource_id <> ''),
    generation bigint NOT NULL CHECK (generation > 0),
    state text NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown','verified')),
    evidence jsonb,
    checked_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    lease_token text,
    lease_until timestamptz,
    CHECK (expires_at >= checked_at AND expires_at <= checked_at + interval '30 seconds'),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((state = 'verified' AND evidence IS NOT NULL AND jsonb_typeof(evidence) = 'object') OR
           (state = 'unknown' AND evidence IS NULL))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS managed_postgres_connection_capacity;
-- +goose StatementEnd
