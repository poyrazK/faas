-- +goose Up
-- +goose StatementBegin
-- filename: 00353_deployments_cancelled.sql
-- ADR-124 deployment queue controls — M1: 'cancelled' status on deployments.
--
-- Closes the API surface gap from docs/adr/089-build-status-endpoint.md
-- (line 35, 90): the closed-value deployments_status_check did not include
-- 'cancelled', so ADR-118's `DeploySuperseded` was the only terminal exit
-- for non-live rows. This migration adds:
--
--   status = 'cancelled'  — terminal state set by user-initiated
--                           POST /v1/apps/{slug}/deployments/{id}/cancel
--                           (and operator-initiated "auto_quota",
--                           "auto_health", "system" reasons)
--   cancelled_at          — wall-clock at the transition
--   cancelled_by_principal — opaque principal string (account owner /
--                            API key id / "operator:<username>")
--   cancel_reason         — closed-set: user|auto_quota|auto_health|system
--   deleted_at            — wall-clock for "deploys clear" soft-delete
--                           (separate audit axis from cancellation)
--   deleted_by_principal  — same opaque principal semantics
--
-- The 'cancelled' value is excluded from billing/storage filters by virtue
-- of the existing predicates (LatestSnapshotBytes: pgstore.go:11554 only
-- reads d.status = 'live'). Cancelled rows are retained for audit but do
-- not block the 'current + previous' snapshot retention rule enforced by
-- the imaged nightly GC.
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_status_check;
ALTER TABLE deployments
  ADD CONSTRAINT deployments_status_check
  CHECK (status IN ('pending', 'building', 'imaging', 'snapshotting',
                    'live', 'failed', 'superseded', 'cancelled'));

ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS cancelled_at timestamptz;
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS cancelled_by_principal text;
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS cancel_reason text;
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS deleted_by_principal text;

ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_cancel_reason_check;
ALTER TABLE deployments
  ADD CONSTRAINT deployments_cancel_reason_check
  CHECK (cancel_reason IS NULL OR cancel_reason IN ('user', 'auto_quota', 'auto_health', 'system'));

-- Index for the list-obsolete query (account-scoped, status filter).
CREATE INDEX IF NOT EXISTS deployments_account_status_cancelled_idx
  ON deployments (account_id, status, cancelled_at DESC)
  WHERE cancelled_at IS NOT NULL OR deleted_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS deployments_account_status_cancelled_idx;
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_cancel_reason_check;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS deleted_by_principal;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS cancel_reason;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS cancelled_by_principal;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS cancelled_at;
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_status_check;
ALTER TABLE deployments
  ADD CONSTRAINT deployments_status_check
  CHECK (status IN ('pending', 'building', 'imaging', 'snapshotting',
                    'live', 'failed', 'superseded'));
-- +goose StatementEnd
