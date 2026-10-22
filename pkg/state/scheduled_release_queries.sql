-- ADR-20261021152201: scheduled releases.

-- name: CancelPendingScheduledReleasesForApp :many
-- Ends the app's pending schedule before another one is recorded.
UPDATE scheduled_releases
   SET state = 'cancelled',
       outcome_code = sqlc.arg(outcome_code)::text,
       outcome_detail = sqlc.arg(outcome_detail)::text,
       updated_at = sqlc.arg(now)::timestamptz
 WHERE app_id = sqlc.arg(app_id)::uuid AND state = 'scheduled'
RETURNING *;

-- name: InsertScheduledRelease :one
INSERT INTO scheduled_releases (
  id, account_id, app_id, deployment_id, release_at, canary, notify_channel_ids,
  override_freeze, override_freeze_ids, scheduled_by, created_at, updated_at
)
VALUES (
  sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(deployment_id)::uuid,
  sqlc.arg(release_at)::timestamptz, sqlc.narg(canary)::jsonb, sqlc.arg(notify_channel_ids)::uuid[],
  sqlc.arg(override_freeze)::text, sqlc.arg(override_freeze_ids)::uuid[], sqlc.arg(scheduled_by)::text,
  sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz
)
RETURNING *;

-- name: GetScheduledRelease :one
SELECT sqlc.embed(sr), d.revision
  FROM scheduled_releases sr
  JOIN deployments d ON d.id = sr.deployment_id
 WHERE sr.id = sqlc.arg(id)::uuid;

-- name: GetLatestScheduledReleaseForDeployment :one
SELECT sqlc.embed(sr), d.revision
  FROM scheduled_releases sr
  JOIN deployments d ON d.id = sr.deployment_id
 WHERE sr.deployment_id = sqlc.arg(deployment_id)::uuid
 ORDER BY sr.created_at DESC, sr.id DESC
 LIMIT 1;

-- name: ListScheduledReleases :many
-- Pending schedules first, soonest first; then the rest, newest first.
SELECT sqlc.embed(sr), d.revision
  FROM scheduled_releases sr
  JOIN deployments d ON d.id = sr.deployment_id
 WHERE sr.account_id = sqlc.arg(account_id)::uuid
   AND (sqlc.narg(app_id)::uuid IS NULL OR sr.app_id = sqlc.narg(app_id)::uuid)
 ORDER BY (sr.state <> 'scheduled'),
          CASE WHEN sr.state = 'scheduled' THEN sr.release_at END,
          sr.updated_at DESC, sr.id
 LIMIT sqlc.arg(max_rows)::integer;

-- name: ListDueScheduledReleases :many
SELECT sqlc.embed(sr), d.revision
  FROM scheduled_releases sr
  JOIN deployments d ON d.id = sr.deployment_id
 WHERE sr.state = 'scheduled' AND sr.release_at <= sqlc.arg(now)::timestamptz
 ORDER BY sr.release_at, sr.id
 LIMIT sqlc.arg(max_rows)::integer;

-- name: FinishScheduledRelease :one
-- Ends a pending schedule without starting it; a schedule that is no longer
-- pending returns no row.
UPDATE scheduled_releases
   SET state = sqlc.arg(state)::text,
       outcome_code = sqlc.arg(outcome_code)::text,
       outcome_detail = sqlc.arg(outcome_detail)::text,
       updated_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id)::uuid AND state = 'scheduled'
RETURNING *;

-- name: LockScheduledRelease :one
SELECT * FROM scheduled_releases WHERE id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: MarkScheduledReleaseStarted :exec
UPDATE scheduled_releases
   SET state = 'started', started_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id)::uuid AND state = 'scheduled';

-- name: ReadScheduledStartDeployment :one
SELECT traffic_percent, canary_total_steps FROM deployments WHERE id = sqlc.arg(id)::uuid;

-- name: StampScheduledCanaryStart :exec
-- Stamps the canary ladder on a parked release that just took its first
-- stage's traffic.
UPDATE deployments
   SET traffic_percent_explicit = false,
       canary_preset = sqlc.arg(canary_preset)::text,
       canary_stages = sqlc.narg(canary_stages)::jsonb,
       canary_total_steps = sqlc.arg(canary_total_steps)::integer,
       canary_step = 0,
       canary_step_started_at = sqlc.arg(now)::timestamptz,
       rollout_state = 'rolling_out',
       rollout_started_at = sqlc.arg(now)::timestamptz,
       rollout_completed_at = NULL
 WHERE id = sqlc.arg(id)::uuid;

-- name: StampScheduledCutoverStart :exec
-- Completes the rollout of a parked release that just took all traffic.
UPDATE deployments
   SET traffic_percent_explicit = false,
       rollout_state = 'complete',
       rollout_started_at = sqlc.arg(now)::timestamptz,
       rollout_completed_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id)::uuid;
