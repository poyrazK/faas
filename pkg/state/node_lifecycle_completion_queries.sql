-- name: ReadNodeLifecycleCompletion :one
SELECT * FROM node_lifecycle_completions WHERE node_id=sqlc.arg(node_id)::uuid AND source_generation=sqlc.arg(generation)::bigint;

-- name: LockNodeLifecycleCompletionTarget :one
SELECT id::text,name,lifecycle::text,lifecycle_generation,drain_initiated_at,recovery_initiated_at
FROM compute_nodes WHERE id=sqlc.arg(node_id)::uuid FOR UPDATE;

-- Capacity policy lock serializes instance admissions, moves and state writes.
-- name: NodeLifecycleCompletionEligible :one
SELECT NOT EXISTS(SELECT 1 FROM migration_leases WHERE source_node_id=sqlc.arg(node_id)::uuid::text) AND NOT EXISTS(SELECT 1 FROM migration_sources WHERE source_node_id=sqlc.arg(node_id)::uuid AND phase IN ('bound','retiring')) AND NOT EXISTS(SELECT 1 FROM migration_admissions WHERE destination_node_id=sqlc.arg(node_id)::uuid AND phase NOT IN ('cleaned','committed')) AND NOT EXISTS (SELECT 1 FROM instances WHERE node_id=sqlc.arg(node_id)::uuid AND
 ((sqlc.arg(lifecycle)::text='recovering' AND state IN ('migrating','snapshotting')) OR
  (sqlc.arg(lifecycle)::text<>'recovering' AND state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen'))))::boolean;

-- name: InsertNodeLifecycleCompletion :one
INSERT INTO node_lifecycle_completions(node_id,source_generation,result_generation,source_lifecycle,result_lifecycle,node_name,initiated_at,completed_at,drained_count,migrated_count,recreated_count,audit_outbox_id)
VALUES(sqlc.arg(node_id)::uuid,sqlc.arg(generation)::bigint,sqlc.arg(result_generation)::bigint,
 sqlc.arg(source_lifecycle)::text,sqlc.arg(result_lifecycle)::text,sqlc.arg(node_name)::text,
 sqlc.arg(initiated_at)::timestamptz,sqlc.arg(completed_at)::timestamptz,
 sqlc.arg(drained_count)::bigint,sqlc.arg(migrated_count)::bigint,sqlc.arg(recreated_count)::bigint,sqlc.arg(outbox_id)::bigint)
RETURNING node_id;

-- name: FinishNodeLifecycleCompletion :one
UPDATE compute_nodes SET lifecycle=sqlc.arg(result_lifecycle)::compute_node_lifecycle,
 drain_completed_at=CASE WHEN sqlc.arg(result_lifecycle)::text='maintenance' THEN sqlc.arg(completed_at)::timestamptz ELSE drain_completed_at END,
 last_recovery_outcome=CASE WHEN sqlc.arg(result_lifecycle)::text='active' THEN 'succeeded' ELSE last_recovery_outcome END
WHERE id=sqlc.arg(node_id)::uuid AND lifecycle_generation=sqlc.arg(generation)::bigint
 AND lifecycle::text=sqlc.arg(source_lifecycle)::text RETURNING lifecycle_generation;
