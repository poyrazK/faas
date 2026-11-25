-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_autovacuum_settings (
    database_id uuid PRIMARY KEY REFERENCES database_vms(database_id) ON DELETE CASCADE,
    profile text NOT NULL CHECK (profile IN ('default', 'high-churn')),
    revision bigint NOT NULL CHECK (revision > 0),
    next_attempt_at timestamptz
);

-- +goose Down
DROP TABLE native_postgres_autovacuum_settings;
