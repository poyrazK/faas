-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_role_verifiers ADD COLUMN pool_revision bigint NOT NULL DEFAULT 0;

CREATE TABLE native_postgres_binding_timeouts (
    binding_id uuid PRIMARY KEY REFERENCES managed_postgres_bindings(id) ON DELETE CASCADE,
    statement_timeout_ms integer NOT NULL CHECK (statement_timeout_ms >= 0),
    lock_timeout_ms integer NOT NULL CHECK (lock_timeout_ms >= 0),
    idle_in_transaction_session_timeout_ms integer NOT NULL CHECK (idle_in_transaction_session_timeout_ms >= 0),
    revision bigint NOT NULL CHECK (revision > 0),
    applied_at timestamptz,
    retry_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX native_postgres_binding_timeouts_pending
    ON native_postgres_binding_timeouts (retry_at) WHERE applied_at IS NULL;

-- +goose Down
DROP TABLE native_postgres_binding_timeouts;
ALTER TABLE database_role_verifiers DROP COLUMN pool_revision;
