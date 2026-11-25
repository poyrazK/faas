-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_maintenance_windows (
    database_id uuid PRIMARY KEY REFERENCES database_vms(database_id) ON DELETE CASCADE,
    weekday smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_minute smallint NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 1 AND 1440),
    stale_since timestamptz
);

CREATE TABLE native_postgres_base_refreshes (
    database_id uuid REFERENCES database_vms(database_id) ON DELETE CASCADE,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    source_running_at timestamptz NOT NULL,
    node_id text NOT NULL,
    started_at timestamptz NOT NULL,
    recover_after timestamptz NOT NULL,
    finished_at timestamptz,
    outcome text NOT NULL DEFAULT '' CHECK (outcome IN ('', 'succeeded', 'still_stale', 'interrupted', 'window_closed', 'runtime_failed')),
    CHECK ((finished_at IS NULL) = (outcome = '')),
    PRIMARY KEY (database_id, window_start)
);

CREATE UNIQUE INDEX native_postgres_base_refreshes_active
    ON native_postgres_base_refreshes (database_id) WHERE finished_at IS NULL;
