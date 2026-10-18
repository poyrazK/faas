-- name: ReadSnapshotRecoveryDeployment :one
SELECT d.id::text AS id, d.app_id::text AS app_id, a.account_id::text AS account_id, d.snapshot_recovery_generation
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.id = sqlc.arg(deployment_id)::uuid AND a.status <> 'deleted';

-- Parent locks follow the same app/deployment order as runtime publication.
-- name: LockSnapshotRecoveryApp :one
SELECT id FROM apps WHERE id = sqlc.arg(app_id)::uuid AND status <> 'deleted' FOR UPDATE;

-- name: LockSnapshotRecoveryDeployment :one
SELECT snapshot_recovery_generation FROM deployments
WHERE id = sqlc.arg(deployment_id)::uuid AND app_id = sqlc.arg(app_id)::uuid FOR UPDATE;

-- name: AdvanceSnapshotRecoveryGeneration :one
UPDATE deployments SET snapshot_recovery_generation = snapshot_recovery_generation + 1,
 snapshot_miss_count = 0, snapshot_miss_backoff_until = NULL
WHERE id = sqlc.arg(deployment_id)::uuid RETURNING snapshot_recovery_generation;

-- name: InvalidateSnapshotRecovery :many
WITH chosen AS MATERIALIZED (
 SELECT id, tier FROM snapshots WHERE deployment_id = sqlc.arg(deployment_id)::uuid AND NOT stale
), replicas AS (
 DELETE FROM snapshot_replicas WHERE snapshot_id IN (SELECT id FROM chosen) RETURNING snapshot_id
)
UPDATE snapshots s SET stale = true FROM chosen c WHERE s.id = c.id RETURNING s.id::text AS id, s.tier;

-- name: SnapshotRecoveryInvalidationComplete :one
SELECT NOT EXISTS (SELECT 1 FROM snapshots WHERE deployment_id = sqlc.arg(deployment_id)::uuid AND NOT stale)::boolean;

-- name: ReadSnapshotRecoveryInstance :one
SELECT app_id::text, deployment_id::text, wake_id::text, state FROM instances
WHERE id = sqlc.arg(instance_id)::uuid;

-- name: LockSnapshotRecoveryInstance :one
SELECT app_id::text, deployment_id::text, wake_id::text, state FROM instances
WHERE id = sqlc.arg(instance_id)::uuid FOR UPDATE;

-- name: ReadInstanceSnapshotRecovery :one
SELECT * FROM instance_snapshot_recovery WHERE instance_id = sqlc.arg(instance_id)::uuid;

-- name: BindInstanceSnapshotRecovery :one
INSERT INTO instance_snapshot_recovery (instance_id, wake_id, generation, snapshot_id)
VALUES (sqlc.arg(instance_id)::uuid, sqlc.arg(wake_id)::uuid, sqlc.arg(generation)::bigint, sqlc.narg(snapshot_id)::uuid)
RETURNING generation;

-- name: SnapshotRecoveryAdmissionValid :one
SELECT EXISTS (SELECT 1 FROM snapshots s JOIN deployments d ON d.id = s.deployment_id
 WHERE s.id = sqlc.arg(snapshot_id)::uuid AND s.deployment_id = sqlc.arg(deployment_id)::uuid
 AND NOT s.stale AND NOT s.delete_pending AND s.recovery_generation = d.snapshot_recovery_generation)::boolean;

-- name: InstanceSnapshotRecoveryFresh :one
SELECT (d.snapshot_recovery_generation = 0 OR EXISTS (
 SELECT 1 FROM instance_snapshot_recovery r WHERE r.instance_id = i.id AND r.wake_id = i.wake_id
 AND r.generation = d.snapshot_recovery_generation))::boolean
FROM instances i JOIN deployments d ON d.id = i.deployment_id WHERE i.id = sqlc.arg(instance_id)::uuid;

-- name: SnapshotRecoveryPublicationGeneration :one
SELECT d.snapshot_recovery_generation FROM deployments d
WHERE d.id = sqlc.arg(deployment_id)::uuid AND (d.snapshot_recovery_generation = 0 OR EXISTS (
 SELECT 1 FROM instances i JOIN instance_snapshot_recovery r ON r.instance_id = i.id AND r.wake_id = i.wake_id
 WHERE i.id = sqlc.narg(instance_id)::uuid AND i.deployment_id = d.id AND r.generation = d.snapshot_recovery_generation));

-- name: CompleteOperatorColdBoot :one
UPDATE operator_intents SET status = sqlc.arg(status)::text, error = sqlc.arg(error)::text,
 finished_at = sqlc.arg(finished_at)::timestamptz, snap_ids_marked_stale = sqlc.arg(snap_ids)::text[],
 cold_boot_generation = sqlc.narg(generation)::bigint, completion_audit_outbox_id = sqlc.arg(outbox_id)::bigint
WHERE id = sqlc.arg(id)::uuid AND claim_token = sqlc.arg(claim_token)::uuid
 AND status = 'running' AND kind = 'force_cold_boot' RETURNING *;

-- name: EnqueueOperatorColdBootOutcome :one
INSERT INTO audit_event_outbox (actor, kind, subject, data, dedupe_key)
VALUES ('schedd', 'operator.action.force_cold_boot.outcome', sqlc.narg(subject)::uuid, sqlc.arg(data)::jsonb, sqlc.arg(dedupe_key)::text)
ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key = excluded.dedupe_key RETURNING id;

-- name: ListOperatorIntentAttemptsByTrace :many
SELECT * FROM operator_intents WHERE trace_id = sqlc.arg(trace_id)::text
ORDER BY requested_at DESC, id DESC LIMIT sqlc.arg(row_limit)::bigint;
