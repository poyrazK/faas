-- filename: 20261125105038100_managed_postgres_wake_with_app.sql
-- migration-compat: expand
--
-- A customer may have a scale-to-zero managed PostgreSQL database start when
-- an app bound to it wakes (gregale postgres wake-with-app,
-- ADR-20261125105038). One row per opted-in database; no row is off.
-- last_started_at is when apid last started the database for a wake: a claim
-- stamps it, so only one apid replica calls the provider per start. apid is
-- the only writer; the row goes with the database. An older apid ignores the
-- table.
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_wake_with_app (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    enabled_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(enabled_at)),
    last_started_at timestamptz CHECK (last_started_at IS NULL OR isfinite(last_started_at))
);
COMMENT ON TABLE managed_postgres_wake_with_app IS 'Managed PostgreSQL databases that start when a bound app wakes (opt-in); no row is off; written only by apid.';

-- +goose Down
DROP TABLE IF EXISTS managed_postgres_wake_with_app;
