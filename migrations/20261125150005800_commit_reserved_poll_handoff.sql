-- filename: 20261125150005800_commit_reserved_poll_handoff.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS relay_reserved_deferred_check_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources DROP COLUMN IF EXISTS relay_reserved_deferred_check_at;
