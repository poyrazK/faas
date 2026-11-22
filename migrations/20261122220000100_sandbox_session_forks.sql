-- migration-compat: expand
-- ADR-20261122132055 §3: forks. A fork is a queued session forked_from its
-- source that restores fork_capture_id: the source's live fork capture, or
-- its pause capture when it was paused. apid sets fork_requested_at on a
-- running source; agentd takes the capture, hands it to the waiting forks
-- and clears it. The previous release never reads these columns.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE sandbox_sessions
  ADD COLUMN IF NOT EXISTS fork_capture_id uuid,
  ADD COLUMN IF NOT EXISTS fork_requested_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS fork_requested_at, DROP COLUMN IF EXISTS fork_capture_id;
