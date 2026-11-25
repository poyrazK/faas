-- filename: 20261125150005100_commit_relay_poll_schedule.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources
    ADD COLUMN IF NOT EXISTS relay_next_poll_at timestamptz NOT NULL DEFAULT '-infinity',
    ADD COLUMN IF NOT EXISTS relay_poll_token uuid,
    ADD COLUMN IF NOT EXISTS relay_poll_backoff_ms integer NOT NULL DEFAULT 0;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_sources
    DROP COLUMN IF EXISTS relay_poll_backoff_ms,
    DROP COLUMN IF EXISTS relay_poll_token,
    DROP COLUMN IF EXISTS relay_next_poll_at;
