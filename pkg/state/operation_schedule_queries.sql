-- ADR-20261018161833 Operation schedules.

-- name: InsertOperationSchedule :one
INSERT INTO customer_operation_schedules(id, account_id, app_id, platform_tenant_id, definition_id, input, kind, at, cron, timezone,
    state, next_fire_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(app_id), sqlc.arg(platform_tenant_id), sqlc.arg(definition_id), sqlc.arg(input),
    sqlc.arg(kind), sqlc.narg(at), sqlc.arg(cron), sqlc.arg(timezone), 'active', sqlc.arg(next_fire_at), sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: GetOperationSchedule :one
SELECT * FROM customer_operation_schedules
WHERE id = sqlc.arg(id) AND account_id = sqlc.arg(account_id)
  AND (sqlc.arg(tenant_id)::text = '' OR platform_tenant_id::text = sqlc.arg(tenant_id)::text);

-- name: LockOperationSchedule :one
SELECT * FROM customer_operation_schedules
WHERE id = sqlc.arg(id) AND account_id = sqlc.arg(account_id)
  AND (sqlc.arg(tenant_id)::text = '' OR platform_tenant_id::text = sqlc.arg(tenant_id)::text)
FOR UPDATE;

-- name: ListOperationSchedules :many
SELECT * FROM customer_operation_schedules
WHERE account_id = sqlc.arg(account_id)
  AND (sqlc.arg(tenant_id)::text = '' OR platform_tenant_id::text = sqlc.arg(tenant_id)::text)
  AND (sqlc.arg(app_id)::text = '' OR app_id::text = sqlc.arg(app_id)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountLiveOperationSchedules :one
SELECT count(*) FROM customer_operation_schedules
WHERE account_id = sqlc.arg(account_id) AND state IN ('active','paused');

-- name: SetOperationScheduleState :one
UPDATE customer_operation_schedules
SET state = sqlc.arg(state), next_fire_at = sqlc.narg(next_fire_at), lease_token = NULL, lease_until = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- Claim due schedules for one firer. A held lease is skipped until it lapses,
-- so a firer that dies mid-fire hands the row to the next tick.
-- name: ClaimDueOperationSchedules :many
UPDATE customer_operation_schedules s
SET lease_token = sqlc.arg(lease_token), lease_until = sqlc.arg(lease_until)
WHERE s.id IN (
    SELECT c.id FROM customer_operation_schedules c
    WHERE c.state = 'active' AND c.next_fire_at <= sqlc.arg(now)
      AND (c.lease_until IS NULL OR c.lease_until < sqlc.arg(now))
    ORDER BY c.next_fire_at, c.id
    LIMIT sqlc.arg(row_limit)
    FOR UPDATE SKIP LOCKED
)
RETURNING s.*;

-- name: LockLeasedOperationSchedule :one
SELECT * FROM customer_operation_schedules
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token) AND state = 'active'
FOR UPDATE;

-- name: RecordOperationScheduleFire :one
UPDATE customer_operation_schedules
SET state = sqlc.arg(state), next_fire_at = sqlc.narg(next_fire_at), fires = fires + sqlc.arg(fired)::bigint,
    last_fired_at = sqlc.arg(fired_at), last_operation_id = COALESCE(sqlc.narg(last_operation_id), last_operation_id),
    last_error = sqlc.arg(last_error), lease_token = NULL, lease_until = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;
