-- filename: 20261124144626100_managed_postgres_suspend_timeouts.sql
-- migration-compat: expand
--
-- A customer may choose how long a scale-to-zero managed PostgreSQL database
-- stays up after its last activity (gregale postgres suspend-timeout). One row
-- per database that has its own value; no row means the platform default
-- (managed_postgres_policy suspend_timeout_seconds). The value is not part of
-- the database's spec, so resize and compute-policy journals never see it;
-- the suspend timeout sweep moves the provider's compute onto it. apid is the
-- only writer; the row goes with the database. An older apid ignores the
-- table and keeps every database on the platform default.
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_suspend_timeouts (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    seconds integer NOT NULL CHECK (seconds BETWEEN 60 AND 3600),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
COMMENT ON TABLE managed_postgres_suspend_timeouts IS 'Customer-chosen scale-to-zero suspend timeout per managed PostgreSQL database; no row is the platform default; written only by apid.';

-- +goose Down
DROP TABLE IF EXISTS managed_postgres_suspend_timeouts;
