-- name: GetOperatorPark :one
SELECT * FROM operator_park_executions WHERE intent_id = sqlc.arg(intent_id)::uuid;

-- name: LockOperatorPark :one
SELECT * FROM operator_park_executions WHERE intent_id = sqlc.arg(intent_id)::uuid FOR UPDATE;

-- name: InsertOperatorPark :one
INSERT INTO operator_park_executions (intent_id,target,phase)
VALUES (sqlc.arg(intent_id)::uuid,sqlc.arg(target)::jsonb,'prepared') RETURNING *;

-- name: StartOperatorPark :one
UPDATE operator_park_executions SET phase='started',effect_token=sqlc.arg(effect_token)::uuid,updated_at=now()
WHERE intent_id=sqlc.arg(intent_id)::uuid AND phase='prepared' RETURNING *;

-- name: AcknowledgeOperatorPark :execrows
UPDATE operator_park_executions SET phase='acknowledged',receipt=sqlc.arg(receipt)::jsonb,stopped_at=now(),updated_at=now()
WHERE intent_id=sqlc.arg(intent_id)::uuid AND effect_token=sqlc.arg(effect_token)::uuid
 AND phase IN ('started','outcome_unknown') AND receipt IS NULL;

-- name: SetOperatorParkPhase :execrows
UPDATE operator_park_executions SET phase=sqlc.arg(phase)::text,terminal_state=sqlc.narg(terminal_state)::text,updated_at=now()
WHERE intent_id=sqlc.arg(intent_id)::uuid;

-- name: LockOperatorParkInstance :one
SELECT id::text,app_id::text,deployment_id::text,node_id::text,wake_id::text AS wake_id,state::text,
 coalesce(lease_token,'')::text AS lease_token,migrated_at,mode,started_at,last_request_at,request_count,exclusive_capture_blocked
FROM instances WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: SetOperatorParkInstanceState :execrows
UPDATE instances SET state=sqlc.arg(state)::text,
 terminal_at=CASE WHEN sqlc.arg(state)::text='stopped' THEN now() ELSE terminal_at END,
 parked_at=CASE WHEN sqlc.arg(state)::text IN ('parked','snapshotting') THEN now() ELSE parked_at END,
 last_request_at=greatest(last_request_at,now())
WHERE id=sqlc.arg(id)::uuid AND state=sqlc.arg(expected_state)::text;

-- name: EnqueueOperatorParkOutcome :one
INSERT INTO audit_event_outbox (actor,kind,subject,data,dedupe_key)
VALUES ('schedd','operator.action.force_park.outcome',sqlc.narg(subject)::uuid,sqlc.arg(data)::jsonb,sqlc.arg(dedupe_key)::text)
ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key=excluded.dedupe_key RETURNING id;

-- name: OperatorParkHoldsInstance :one
SELECT EXISTS (SELECT 1 FROM operator_park_executions e JOIN instances i ON i.id=(e.target->>'instance_id')::uuid
 AND i.node_id=(e.target->>'node_id')::uuid AND i.wake_id=(e.target->>'wake_id')::uuid
 WHERE i.id=sqlc.arg(instance_id)::uuid AND e.phase IN ('prepared','started','acknowledged','outcome_unknown'))::boolean;

-- name: EnqueueOperatorParkSnapshot :one
INSERT INTO notification_outbox (channel,payload) VALUES ('snapshot_written',sqlc.arg(payload)::text) RETURNING id;

-- name: NotifyOperatorParkSnapshot :exec
SELECT pg_notify('snapshot_written',sqlc.arg(payload)::text);

-- name: SetOperatorParkBarrier :execrows
UPDATE instances SET exclusive_capture_blocked=sqlc.arg(blocked)::boolean WHERE id=sqlc.arg(id)::uuid;
