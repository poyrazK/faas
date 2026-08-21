-- +goose Up
-- +goose StatementBegin
-- filename: 00354_builds_cancelled.sql
-- ADR-124 deployment queue controls — M2: 'cancelled' status on builds.
--
-- Pair to M1. The build-row state machine (pkg/state/types.go:199-204)
-- is the natural seam for the new cancel signal because the build VM
-- lifecycle is owned by builderd (pkg/builderd/vm_metal.go:119 Spawn →
-- :229 WaitForCompletion). builderd's cancel-LISTEN goroutine consumes
-- the deployment_changed pg_notify payload, races the build row to
-- 'cancelled', then issues VMMDriver.Cancel → vmmd.Manager.CancelBuild.
--
-- `cancelled_by_deployment_cascade` disambiguates two cancel sources:
--   * true  — the cancel came from CancelDeploymentTx (deployment row
--             flipped first; build follows via cascade). Builderd MUST
--             kill the live VM.
--   * false — a future direct build-cancel path. This PR does not
--             expose one; the column documents the intent so a
--             follow-up ADR can flip it without re-migrating.
--
-- The CHECK widening mirrors M1 exactly so that pg_notify consumers can
-- treat deployments.status = 'cancelled' as authoritative for the
-- combined "deployment row gone, build row gone" outcome.
ALTER TABLE builds
  DROP CONSTRAINT IF EXISTS builds_status_check;
ALTER TABLE builds
  ADD CONSTRAINT builds_status_check
  CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled'));

ALTER TABLE builds
  ADD COLUMN IF NOT EXISTS cancelled_at timestamptz;
ALTER TABLE builds
  ADD COLUMN IF NOT EXISTS cancelled_by_deployment_cascade bool NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS builds_deployment_status_cancelled_idx
  ON builds (deployment_id, status, cancelled_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS builds_deployment_status_cancelled_idx;
-- Cancellation sets must precede any 'cancelled' rows for the narrower CHECK.
UPDATE builds SET status = 'failed' WHERE status = 'cancelled';
ALTER TABLE builds
  DROP COLUMN IF EXISTS cancelled_by_deployment_cascade;
ALTER TABLE builds
  DROP COLUMN IF EXISTS cancelled_at;
ALTER TABLE builds
  DROP CONSTRAINT IF EXISTS builds_status_check;
ALTER TABLE builds
  ADD CONSTRAINT builds_status_check
  CHECK (status IN ('queued', 'running', 'succeeded', 'failed'));
-- +goose StatementEnd
