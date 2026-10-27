-- ADR-20261023181931: rollbacks that restore the database.

-- name: InsertReleaseDatabaseSwitch :one
-- No row when the account already runs max_active unfinished switches
-- (api.DatabaseSwitchesActivePerAccount).
INSERT INTO release_database_switches (
  id, account_id, app_id, scope, from_deployment_id, target_deployment_id, undone_deployment_ids,
  source_database_id, target_name, restore_point, phase, requested_by,
  phase_started_at, retry_at, created_at, updated_at
)
SELECT
  sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text,
  sqlc.narg(from_deployment_id)::uuid, sqlc.arg(target_deployment_id)::uuid, sqlc.arg(undone_deployment_ids)::uuid[],
  sqlc.arg(source_database_id)::uuid, sqlc.arg(target_name)::text, sqlc.arg(restore_point)::timestamptz,
  'restoring', sqlc.arg(requested_by)::text,
  sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz
 WHERE (SELECT count(*) FROM release_database_switches active
         WHERE active.account_id = sqlc.arg(account_id)::uuid
           AND active.phase NOT IN ('completed', 'failed', 'cancelled')) < sqlc.arg(max_active)::integer
RETURNING *;

-- name: GetReleaseDatabaseSwitch :one
SELECT * FROM release_database_switches
 WHERE id = sqlc.arg(id)::uuid AND account_id = sqlc.arg(account_id)::uuid;

-- name: ListReleaseDatabaseSwitches :many
SELECT * FROM release_database_switches
 WHERE account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(max_rows)::integer;

-- name: ClaimDueReleaseDatabaseSwitches :many
-- Leases due active switches. SKIP LOCKED keeps two apid workers from
-- waiting on each other's claim.
UPDATE release_database_switches sw
   SET lease_token = sqlc.arg(token)::text,
       lease_until = sqlc.arg(lease_until)::timestamptz,
       updated_at = sqlc.arg(now)::timestamptz
  FROM (
    SELECT id FROM release_database_switches
     WHERE phase NOT IN ('completed', 'failed', 'cancelled')
       AND retry_at <= sqlc.arg(now)::timestamptz
       AND (lease_until IS NULL OR lease_until <= sqlc.arg(now)::timestamptz)
     ORDER BY retry_at, id
     LIMIT sqlc.arg(max_rows)::integer
     FOR UPDATE SKIP LOCKED
  ) due
 WHERE sw.id = due.id
RETURNING sw.*;

-- name: SaveReleaseDatabaseSwitch :one
-- A cancel the API accepted while the step ran holds the switch in its
-- cancellable phase: the drain's advance to switching is not saved and the
-- switch is due at once, so the next step aborts it.
UPDATE release_database_switches
   SET target_database_id = sqlc.narg(target_database_id)::uuid,
       cutover_id = sqlc.narg(cutover_id)::uuid,
       phase = CASE WHEN cancel_requested_at IS NOT NULL AND phase IN ('restoring', 'staging', 'verifying', 'fencing', 'settling', 'draining') AND sqlc.arg(phase)::text = 'switching'
                    THEN phase ELSE sqlc.arg(phase)::text END,
       failure_code = sqlc.arg(failure_code)::text,
       failure_detail = sqlc.arg(failure_detail)::text,
       last_error_code = sqlc.arg(last_error_code)::text,
       fenced_at = sqlc.narg(fenced_at)::timestamptz,
       drain_requested_at = sqlc.narg(drain_requested_at)::timestamptz,
       release_switched_at = sqlc.narg(release_switched_at)::timestamptz,
       activated_at = sqlc.narg(activated_at)::timestamptz,
       finished_at = sqlc.narg(finished_at)::timestamptz,
       phase_started_at = CASE WHEN cancel_requested_at IS NOT NULL AND phase IN ('restoring', 'staging', 'verifying', 'fencing', 'settling', 'draining') AND sqlc.arg(phase)::text = 'switching'
                               THEN phase_started_at ELSE sqlc.arg(phase_started_at)::timestamptz END,
       drain_retired = sqlc.arg(drain_retired)::integer,
       drain_parked = sqlc.arg(drain_parked)::integer,
       drain_pending = sqlc.arg(drain_pending)::integer,
       drain_blocked = sqlc.arg(drain_blocked)::integer,
       attempt_count = sqlc.arg(attempt_count)::integer,
       retry_at = CASE WHEN cancel_requested_at IS NOT NULL AND phase IN ('restoring', 'staging', 'verifying', 'fencing', 'settling', 'draining') AND sqlc.arg(phase)::text = 'switching'
                       THEN sqlc.arg(now)::timestamptz ELSE sqlc.arg(retry_at)::timestamptz END,
       lease_token = NULL,
       lease_until = NULL,
       updated_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id)::uuid
   AND lease_token = sqlc.arg(token)::text
   AND lease_until > sqlc.arg(now)::timestamptz
RETURNING *;

-- name: RequestReleaseDatabaseSwitchCancel :one
UPDATE release_database_switches
   SET cancel_requested_at = coalesce(cancel_requested_at, sqlc.arg(now)::timestamptz),
       retry_at = sqlc.arg(now)::timestamptz,
       updated_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
   AND phase IN ('restoring', 'staging', 'verifying', 'fencing', 'settling', 'draining')
RETURNING *;

-- name: GetReleaseDatabaseSwitchForApp :one
SELECT * FROM release_database_switches
 WHERE id = sqlc.arg(id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid;

-- name: ListDrainingReleaseDatabaseSwitches :many
SELECT * FROM release_database_switches
 WHERE phase = 'draining' AND drain_requested_at IS NOT NULL AND cutover_id IS NOT NULL
 ORDER BY drain_requested_at, id
 LIMIT sqlc.arg(max_rows)::integer;

-- name: ListReleasesUndoneByDatabaseSwitches :many
SELECT DISTINCT undone::text AS deployment_id
  FROM release_database_switches sw, unnest(sw.undone_deployment_ids) AS undone
 WHERE sw.app_id = sqlc.arg(app_id)::uuid AND sw.scope = sqlc.arg(scope)::text AND sw.phase = 'completed'
 ORDER BY 1;
