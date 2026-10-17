-- Capacity statement triggers lock this singleton before node/instance rows.
-- Take it first and hold it through the retirement precondition and transition.
-- name: LockOperatorNodeCapacity :one
SELECT singleton FROM service_capacity_policy WHERE singleton FOR UPDATE;

-- name: LockOperatorNodeTarget :one
SELECT id::text, name, lifecycle::text FROM compute_nodes WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: OperatorNodeHasLiveInstances :one
SELECT EXISTS (SELECT 1 FROM instances WHERE node_id=sqlc.arg(id)::uuid
 AND state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen'))::boolean;

-- name: ReadOperatorNodeExecution :one
SELECT * FROM operator_node_executions WHERE intent_id=sqlc.arg(id)::uuid;

-- name: InsertOperatorNodeExecution :one
INSERT INTO operator_node_executions (intent_id,claim_token,kind,target_id,previous_lifecycle,result_lifecycle,changed,status,error,finished_at,audit_outbox_id)
VALUES (sqlc.arg(id)::uuid,sqlc.arg(claim_token)::uuid,sqlc.arg(kind)::text,sqlc.arg(target_id)::text,
 sqlc.arg(previous_lifecycle)::text,sqlc.arg(result_lifecycle)::text,sqlc.arg(changed)::boolean,
 sqlc.arg(status)::text,sqlc.arg(error)::text,sqlc.arg(finished_at)::timestamptz,sqlc.arg(outbox_id)::bigint)
RETURNING intent_id;

-- name: CompleteOperatorNode :one
UPDATE operator_intents SET status=sqlc.arg(status)::text,error=sqlc.arg(error)::text,
 finished_at=sqlc.arg(finished_at)::timestamptz,completion_audit_outbox_id=sqlc.arg(outbox_id)::bigint,snap_ids_marked_stale='{}'::text[]
WHERE id=sqlc.arg(id)::uuid AND claim_token=sqlc.arg(claim_token)::uuid AND status='running'
 AND kind IN ('node_drain','node_force_drain','node_activate','node_retire') RETURNING id;

-- name: EnqueueOperatorNodeOutcome :one
INSERT INTO audit_event_outbox (actor,kind,subject,data,dedupe_key)
VALUES ('schedd',sqlc.arg(kind)::text,sqlc.narg(subject)::uuid,sqlc.arg(data)::jsonb,sqlc.arg(dedupe_key)::text)
ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key=excluded.dedupe_key RETURNING id;
