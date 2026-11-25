-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_error_history_settings (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    epoch uuid NOT NULL UNIQUE,
    enabled boolean NOT NULL
);

CREATE TABLE native_postgres_error_events (
    id uuid PRIMARY KEY,
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL,
    severity text NOT NULL CHECK (severity IN ('ERROR', 'FATAL', 'PANIC')),
    sqlstate text NOT NULL CHECK (sqlstate ~ '^[A-Z0-9]{5}$'),
    binding_id uuid
);

CREATE INDEX native_postgres_error_events_history
    ON native_postgres_error_events (database_id, observed_at DESC, id DESC);
CREATE INDEX native_postgres_error_events_expiry
    ON native_postgres_error_events (observed_at);

-- +goose Down
DROP TABLE native_postgres_error_events;
DROP TABLE native_postgres_error_history_settings;
