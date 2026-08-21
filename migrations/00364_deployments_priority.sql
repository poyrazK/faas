-- +goose Up
-- +goose StatementBegin
-- filename: 00364_deployments_priority.sql
-- ADR-124 deployment queue controls — M3: priority column on deployments.
--
-- Reorder + deploy-immediately surface. `priority` is an int in [0,1000]
-- where lower numbers run first. Default = 100 (current behaviour, FIFO).
-- The 0..1000 range is wide enough for a "deploy immediately" bump (0)
-- through "background rebuild" (1000) without colliding with the
-- existing fairness ordering.
--
-- The partial index covers the only claim path that matters:
--   WHERE status = 'pending' ORDER BY priority ASC, created_at ASC
-- which builderd reads when the dashboard's "deploy-immediately" button
-- bumps a queued row's priority. Existing FIFO claimers that ignore
-- priority still work — the index is a prefix and the (priority,
-- created_at) tuple orders correctly when priorities are equal.
-- (Note: this index uses `created_at` rather than `enqueued_at` —
-- `enqueued_at` is a `builds` column from migrations/00027; on
-- deployments, `created_at` is the row's submission timestamp and is
-- semantically the queue-arrival time.)
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS priority int NOT NULL DEFAULT 100;

ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_priority_check;
ALTER TABLE deployments
  ADD CONSTRAINT deployments_priority_check
  CHECK (priority BETWEEN 0 AND 1000);

CREATE INDEX IF NOT EXISTS deployments_pending_priority_idx
  ON deployments (app_id, priority, created_at)
  WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS deployments_pending_priority_idx;
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_priority_check;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS priority;
-- +goose StatementEnd
