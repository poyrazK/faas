-- name: ClaimOperatorIntentAttempt :one
WITH candidate AS (
 SELECT i.id FROM operator_intents i
 WHERE i.status = 'pending' OR (i.status = 'outcome_unknown' AND i.kind = 'force_restart'
   AND EXISTS (SELECT 1 FROM operator_restart_executions e JOIN instances v
     ON v.id = (e.target->>'instance_id')::uuid AND v.node_id = (e.target->>'node_id')::uuid
     AND v.wake_id = (e.target->>'wake_id')::uuid AND v.state IN ('running', 'stopped')
     AND coalesce(v.lease_token, '') = coalesce(e.target->>'migration_lease_token', '')
     AND v.migrated_at IS NOT DISTINCT FROM (e.target->>'migrated_at')::timestamptz
     WHERE e.intent_id = i.id AND e.phase = 'destroyed')) OR
 (i.status = 'outcome_unknown' AND i.kind = 'force_park' AND EXISTS (
  SELECT 1 FROM operator_park_executions e JOIN instances v
   ON v.id=(e.target->>'instance_id')::uuid AND v.node_id=(e.target->>'node_id')::uuid
   AND v.wake_id=(e.target->>'wake_id')::uuid
   AND v.mode=e.target->>'mode' AND v.state IN (CASE WHEN e.target->>'action'='capture' THEN 'snapshotting' ELSE e.target->>'initial_state' END, e.receipt->>'outcome')
   AND coalesce(v.lease_token,'')=coalesce(e.target->>'migration_lease_token','')
   AND v.migrated_at IS NOT DISTINCT FROM (e.target->>'migrated_at')::timestamptz
   AND v.started_at IS NOT DISTINCT FROM (e.target->>'started_at')::timestamptz
  WHERE e.intent_id=i.id AND e.phase='acknowledged'))
 ORDER BY i.requested_at, i.id FOR UPDATE OF i SKIP LOCKED LIMIT 1
)
UPDATE operator_intents i SET status = 'running', started_at = now(), finished_at = NULL,
 error = NULL, claim_token = sqlc.arg(claim_token)::uuid
FROM candidate c WHERE i.id = c.id RETURNING i.*;

-- name: GetOperatorIntentAttempt :one
SELECT * FROM operator_intents WHERE id = sqlc.arg(id)::uuid;

-- name: LockOperatorIntentAttempt :one
SELECT * FROM operator_intents WHERE id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: ReclaimOperatorIntentAttempts :execrows
UPDATE operator_intents SET status = 'pending', started_at = NULL, claim_token = NULL
WHERE status = 'running' AND started_at < sqlc.arg(threshold)::timestamptz;

-- name: GetOperatorRestart :one
SELECT * FROM operator_restart_executions WHERE intent_id = sqlc.arg(intent_id)::uuid;

-- name: LockOperatorRestart :one
SELECT * FROM operator_restart_executions WHERE intent_id = sqlc.arg(intent_id)::uuid FOR UPDATE;

-- name: InsertOperatorRestart :one
INSERT INTO operator_restart_executions (intent_id, target, phase)
VALUES (sqlc.arg(intent_id)::uuid, sqlc.arg(target)::jsonb, 'prepared') RETURNING *;

-- name: StartOperatorRestartDestroy :one
UPDATE operator_restart_executions SET phase = 'started', destroy_token = sqlc.arg(destroy_token)::uuid, updated_at = now()
WHERE intent_id = sqlc.arg(intent_id)::uuid AND phase = 'prepared' RETURNING *;

-- name: AcknowledgeOperatorRestartDestroy :execrows
UPDATE operator_restart_executions SET
 phase = CASE WHEN phase = 'completed' THEN 'completed' ELSE 'destroyed' END,
 destroyed_at = coalesce(destroyed_at, now()), updated_at = now()
WHERE intent_id = sqlc.arg(intent_id)::uuid AND destroy_token = sqlc.arg(destroy_token)::uuid
 AND phase IN ('started', 'outcome_unknown', 'destroyed', 'completed');

-- name: SetOperatorRestartPhase :exec
UPDATE operator_restart_executions SET phase = sqlc.arg(phase)::text, updated_at = now()
WHERE intent_id = sqlc.arg(intent_id)::uuid;

-- name: LockOperatorRestartInstance :one
SELECT id::text, app_id::text, deployment_id::text, node_id::text, wake_id::text AS wake_id, state::text,
 coalesce(lease_token, '')::text AS lease_token, migrated_at
FROM instances WHERE id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: StopOperatorRestartInstance :execrows
UPDATE instances SET state = 'stopped', terminal_at = now(), last_request_at = greatest(last_request_at, now())
WHERE id = sqlc.arg(id)::uuid AND state = 'running' AND node_id = sqlc.arg(node_id)::uuid
 AND wake_id = sqlc.arg(wake_id)::uuid;

-- name: ReleaseOperatorRestartHostPorts :exec
DELETE FROM container_host_port_leases
WHERE node_id = sqlc.arg(node_id)::uuid AND instance_id = sqlc.arg(instance_id)::uuid;

-- name: InvalidateOperatorRestartSnapshots :many
WITH chosen AS (
 SELECT DISTINCT ON (tier) id, tier FROM snapshots
 WHERE deployment_id = sqlc.arg(deployment_id)::uuid AND tier IN ('warm', 'init') AND NOT stale
 ORDER BY tier, created_at DESC, id DESC
), replicas AS (
 DELETE FROM snapshot_replicas WHERE snapshot_id IN (SELECT id FROM chosen) RETURNING snapshot_id
)
UPDATE snapshots s SET stale = true FROM chosen c WHERE s.id = c.id RETURNING s.id::text AS id, s.tier;

-- name: CompleteOperatorRestartIntent :one
UPDATE operator_intents SET status = sqlc.arg(status)::text, error = sqlc.arg(error)::text,
 snap_ids_marked_stale = sqlc.arg(snap_ids)::text[], finished_at = sqlc.arg(finished_at)::timestamptz,
 completion_audit_outbox_id = sqlc.arg(outbox_id)::bigint
WHERE id = sqlc.arg(id)::uuid RETURNING *;

-- name: EnqueueOperatorRestartOutcome :one
INSERT INTO audit_event_outbox (actor, kind, subject, data, dedupe_key)
VALUES ('schedd', 'operator.action.force_restart.outcome', sqlc.narg(subject)::uuid, sqlc.arg(data)::jsonb, sqlc.arg(dedupe_key)::text)
ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key = excluded.dedupe_key RETURNING id;

-- name: OperatorRestartDatabaseTime :one
SELECT now()::timestamptz;

-- name: CountUnresolvedOperatorIntents :many
SELECT kind, count(*)::bigint AS count FROM operator_intents
WHERE (status = 'running' AND started_at < sqlc.arg(threshold)::timestamptz) OR status = 'outcome_unknown'
GROUP BY kind;
