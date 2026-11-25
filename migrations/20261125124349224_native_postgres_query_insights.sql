-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_query_settings (
    database_id uuid PRIMARY KEY REFERENCES database_vms(database_id) ON DELETE CASCADE,
    database_name text NOT NULL,
    enabled boolean NOT NULL,
    updated_at timestamptz NOT NULL,
    attempted_at timestamptz,
    sampled_at timestamptz,
    baseline jsonb
);

CREATE TABLE native_postgres_query_intervals (
    database_id uuid NOT NULL REFERENCES native_postgres_query_settings(database_id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    ended_at timestamptz NOT NULL,
    boot_at timestamptz NOT NULL,
    counters jsonb NOT NULL,
    PRIMARY KEY (database_id, ended_at),
    CHECK (started_at < ended_at)
);

CREATE INDEX native_postgres_query_intervals_expiry_idx ON native_postgres_query_intervals (ended_at);

-- +goose Down
DROP TABLE native_postgres_query_intervals;
DROP TABLE native_postgres_query_settings;
