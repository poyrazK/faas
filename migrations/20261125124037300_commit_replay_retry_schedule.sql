-- filename: 20261125124037300_commit_replay_retry_schedule.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_replay_requests
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_replay_requests DROP COLUMN IF EXISTS next_attempt_at;
