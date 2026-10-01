-- filename: 20261001134628993_managed_postgres_health.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE managed_postgres_health (
 database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 backend_id text NOT NULL,
 backend_fingerprint text NOT NULL CHECK (length(backend_fingerprint) = 64),
 provider_resource_id text NOT NULL CHECK (length(provider_resource_id) > 0),
 desired_generation bigint NOT NULL CHECK (desired_generation > 0),
 provider_status text NOT NULL DEFAULT 'unknown' CHECK (provider_status IN ('unknown','missing','pending','ready','deleting','failed')),
 compute_state text NOT NULL DEFAULT 'unknown' CHECK (compute_state IN ('unknown','active','suspended','waking')),
 checked_at timestamptz,
 last_success_at timestamptz,
 last_error_code text CHECK (last_error_code IN ('resource_missing','observer_unsupported','provider_unavailable','backend_unavailable','observation_invalid','spec_mismatch','provider_failed')),
 next_check_at timestamptz NOT NULL,
 lease_token text,
 lease_until timestamptz,
 attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 20),
 CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
 CHECK (last_success_at IS NULL OR (checked_at IS NOT NULL AND last_success_at <= checked_at))
);
CREATE INDEX managed_postgres_health_next_check_idx ON managed_postgres_health(next_check_at, database_id);
CREATE INDEX managed_postgres_health_account_idx ON managed_postgres_health(account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE managed_postgres_health;
-- +goose StatementEnd
