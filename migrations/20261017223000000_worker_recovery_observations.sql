-- Scheduler observations are diagnostic, never desired-capacity authority.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE worker_recovery ADD COLUMN pools jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(pools) = 'array');

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE worker_recovery DROP COLUMN pools;
