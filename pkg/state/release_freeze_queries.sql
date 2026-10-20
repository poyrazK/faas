-- ADR-20261019071733: deploy freezes and release windows.

-- name: CreateReleaseFreezeWithinLimit :one
-- Inserts a freeze only while the account holds fewer than max_unlifted
-- unlifted freezes that have not ended.
INSERT INTO release_freezes (
  id, account_id, app_id, kind, starts_at, ends_at, weekly_days, weekly_start_minute,
  weekly_duration_minutes, time_zone, after_rollback_minutes, reason, source,
  source_deployment_id, created_by, created_at
)
SELECT sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.narg(app_id)::uuid, sqlc.arg(kind)::text,
  sqlc.narg(starts_at)::timestamptz, sqlc.narg(ends_at)::timestamptz, sqlc.narg(weekly_days)::smallint,
  sqlc.narg(weekly_start_minute)::smallint, sqlc.narg(weekly_duration_minutes)::integer,
  sqlc.narg(time_zone)::text, sqlc.narg(after_rollback_minutes)::integer, sqlc.arg(reason)::text,
  'manual', NULL, sqlc.arg(created_by)::text, sqlc.arg(now)::timestamptz
WHERE (
  SELECT count(*) FROM release_freezes f
   WHERE f.account_id = sqlc.arg(account_id)::uuid AND f.lifted_at IS NULL
     AND (f.kind <> 'window' OR f.ends_at IS NULL OR f.ends_at > sqlc.arg(now)::timestamptz)
) < sqlc.arg(max_unlifted)::bigint
RETURNING *;

-- name: CreateRollbackReleaseFreeze :execrows
-- One automatic freeze per rolled-back deployment; a retried rollback reuses it.
INSERT INTO release_freezes (
  id, account_id, app_id, kind, starts_at, ends_at, reason, source, source_deployment_id, created_by, created_at
) VALUES (
  sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, 'window',
  sqlc.arg(starts_at)::timestamptz, sqlc.arg(ends_at)::timestamptz, sqlc.arg(reason)::text,
  'automatic_rollback', sqlc.arg(source_deployment_id)::uuid, sqlc.arg(created_by)::text, sqlc.arg(starts_at)::timestamptz
)
ON CONFLICT (source_deployment_id) WHERE source = 'automatic_rollback' DO NOTHING;

-- name: ListUnliftedReleaseFreezes :many
-- Unlifted freezes that have not ended: active and upcoming windows, weekly
-- windows and after-rollback policies.
SELECT * FROM release_freezes
 WHERE account_id = sqlc.arg(account_id)::uuid AND lifted_at IS NULL
   AND (kind <> 'window' OR ends_at IS NULL OR ends_at > sqlc.arg(now)::timestamptz)
 ORDER BY created_at, id;

-- name: LiftReleaseFreeze :one
UPDATE release_freezes
   SET lifted_at = sqlc.arg(now)::timestamptz, lifted_by = sqlc.arg(lifted_by)::text
 WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid AND lifted_at IS NULL
RETURNING *;
