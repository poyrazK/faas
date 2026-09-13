-- +goose Up
-- Claiming a tombstone before deleting external artifacts closes the race with
-- a restore whose statement timestamp still falls inside the grace window.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS purge_claimed_at timestamptz;

-- +goose Down
ALTER TABLE apps DROP COLUMN IF EXISTS purge_claimed_at;
