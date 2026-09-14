-- +goose Up
-- A stale snapshot can mean "temporarily incompatible, keep for rollback" or
-- "GC has committed to deleting its remote artifacts". Keep those states
-- separate so a registry outage leaves a durable, immediately retryable
-- tombstone without shortening the ordinary stale-snapshot retention window.
ALTER TABLE snapshots
    ADD COLUMN IF NOT EXISTS delete_pending boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS snapshots_delete_pending_idx
    ON snapshots (created_at)
    WHERE delete_pending = true;

-- +goose Down
DROP INDEX IF EXISTS snapshots_delete_pending_idx;
ALTER TABLE snapshots DROP COLUMN IF EXISTS delete_pending;
