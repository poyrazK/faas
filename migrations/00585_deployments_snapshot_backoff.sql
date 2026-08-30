-- filename: 00585_deployments_snapshot_backoff.sql
-- Workstream B (issue #1184): snapshot cache miss backoff state on
-- deployments. When WakeJob or Engine.Wake fails the snapshot-fetch
-- path, the deployment's snapshot_miss_count increments and a
-- backoff_until timestamp is stamped so subsequent wakes short-circuit
-- to Retry-After without retry-storming the failing destination.
--
-- Why on deployments (not apps): a deployment's snapshot is its
-- specific build artifact; backoff is per-artifact, not per-app.
-- Cleared automatically on snapshot_miss_count == 0 reset by the
-- recovery arbiter when the destination recovers (commit: feat(sched):
-- recovery arbiter).
--
-- Partial index covers rows currently in backoff so the wake flow's
-- `WHERE id = $1 AND snapshot_miss_backoff_until > now()` check is
-- index-only and never touches the heap.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS snapshot_miss_count          int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS snapshot_miss_last_at        timestamptz NULL,
    ADD COLUMN IF NOT EXISTS snapshot_miss_backoff_until  timestamptz NULL;

CREATE INDEX IF NOT EXISTS deployments_snapshot_backoff_idx
    ON deployments (id) WHERE snapshot_miss_backoff_until IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS deployments_snapshot_backoff_idx;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS snapshot_miss_backoff_until,
    DROP COLUMN IF EXISTS snapshot_miss_last_at,
    DROP COLUMN IF EXISTS snapshot_miss_count;
-- +goose StatementEnd