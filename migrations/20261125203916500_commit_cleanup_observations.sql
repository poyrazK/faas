-- filename: 20261125203916500_commit_cleanup_observations.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS cleanup_eligible_events bigint,
    ADD COLUMN IF NOT EXISTS cleanup_observed_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources DROP COLUMN IF EXISTS cleanup_observed_at, DROP COLUMN IF EXISTS cleanup_eligible_events;
