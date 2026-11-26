-- filename: 20261125203916900_commit_relay_connection_observations.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS relay_connect_ms integer,
    ADD COLUMN IF NOT EXISTS relay_connect_observed_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources DROP COLUMN IF EXISTS relay_connect_observed_at, DROP COLUMN IF EXISTS relay_connect_ms;
