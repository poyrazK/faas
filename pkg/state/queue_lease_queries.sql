-- ADR-1140: production pull leases for worker and job queue consumers.

-- name: QueueLeaseLockBinding :one
-- Serializes concurrent lease calls for one binding so max_concurrency holds.
SELECT b.id, b.queue_name, b.max_concurrency FROM queue_bindings b
WHERE b.account_id=sqlc.arg(account_id)::uuid AND b.app_id=sqlc.arg(app_id)::uuid
    AND b.queue_name=sqlc.arg(queue_name)::text AND b.deployment_scope='' AND b.mode='pull'
    AND b.enabled AND b.retired_at IS NULL
ORDER BY b.created_at, b.id LIMIT 1 FOR UPDATE;

-- name: QueueLeaseActiveCount :one
SELECT count(*)::bigint FROM invocations i
WHERE i.app_id=sqlc.arg(app_id)::uuid AND i.source='queue' AND i.environment_id IS NULL
    AND i.state='dispatching' AND i.lease_expires_at>clock_timestamp()
    AND (i.queue_binding_id=sqlc.arg(binding_id)::uuid
        OR (i.queue_binding_id IS NULL AND i.queue_name=sqlc.arg(queue_name)::text));

-- name: QueueLeaseCandidates :many
SELECT w.id FROM production_invocation_work w
WHERE w.app_id=sqlc.arg(app_id)::uuid AND w.source='queue' AND w.state='pending'
    AND w.due_at<=clock_timestamp() AND (w.deadline_at IS NULL OR w.deadline_at>clock_timestamp())
    AND w.work_policy_name IS NULL AND NOT w.quota_reserved
    AND (w.queue_binding_id=sqlc.arg(binding_id)::uuid
        OR (w.queue_binding_id IS NULL AND w.queue_name=sqlc.arg(queue_name)::text))
ORDER BY w.due_at, w.created_at, w.id LIMIT sqlc.arg(candidate_limit)::integer;

-- name: QueueLeaseClaimInvocation :one
WITH lease_clock AS MATERIALIZED (SELECT clock_timestamp() AS at)
UPDATE invocations i SET state='dispatching', received_at=COALESCE(i.received_at, lease_clock.at),
    lease_expires_at=lease_clock.at+make_interval(secs => sqlc.arg(lease_seconds)::integer),
    attempts=i.attempts+1, instance_id=sqlc.narg(instance_id)::text
FROM lease_clock
WHERE i.id=sqlc.arg(id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid AND i.source='queue'
    AND i.state='pending' AND NOT i.quota_reserved AND i.environment_id IS NULL AND i.work_policy_name IS NULL
    AND i.due_at<=lease_clock.at AND (i.deadline_at IS NULL OR i.deadline_at>lease_clock.at)
RETURNING i.*;

-- name: QueueLeaseExhaustInvocation :execrows
-- A message whose leases kept expiring has no attempts left to lease.
UPDATE invocations SET state='dead_letter', outcome='dead_letter', completed_at=clock_timestamp(),
    last_error='queue lease attempt budget exhausted after lease expiry', lease_expires_at=NULL, instance_id=NULL
WHERE id=sqlc.arg(id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND state='pending' AND attempts=sqlc.arg(attempt)::integer;

-- name: UpsertQueueLeaseReceipt :exec
INSERT INTO invocation_queue_lease_receipts(invocation_id, attempt, replay_generation, token_hash, issued_at, lease_expires_at)
VALUES (sqlc.arg(invocation_id)::uuid, sqlc.arg(attempt)::integer, sqlc.arg(replay_generation)::bigint,
    sqlc.arg(token_hash)::text, clock_timestamp(), sqlc.arg(lease_expires_at)::timestamptz)
ON CONFLICT (invocation_id) DO UPDATE SET attempt=excluded.attempt, replay_generation=excluded.replay_generation,
    token_hash=excluded.token_hash, issued_at=excluded.issued_at, lease_expires_at=excluded.lease_expires_at;

-- name: ReadQueueLeaseReceipt :one
SELECT * FROM invocation_queue_lease_receipts WHERE invocation_id=$1;

-- name: DeleteQueueLeaseReceipt :exec
DELETE FROM invocation_queue_lease_receipts WHERE invocation_id=$1;

-- name: LockQueueLeaseInvocation :one
SELECT i.* FROM invocations i WHERE i.id=$1 FOR UPDATE OF i;

-- name: QueueLeaseClock :one
SELECT clock_timestamp()::timestamptz AS wall_time;

-- name: ExtendQueueLeaseInvocation :one
UPDATE invocations SET lease_expires_at=clock_timestamp()+make_interval(secs => sqlc.arg(lease_seconds)::integer)
WHERE id=sqlc.arg(id)::uuid AND state='dispatching' AND attempts=sqlc.arg(attempt)::integer
    AND replay_generation=sqlc.arg(replay_generation)::bigint AND lease_expires_at>clock_timestamp()
RETURNING lease_expires_at;

-- name: ExtendQueueLeaseReceipt :exec
UPDATE invocation_queue_lease_receipts SET lease_expires_at=sqlc.arg(lease_expires_at)::timestamptz
WHERE invocation_id=sqlc.arg(invocation_id)::uuid;

-- name: RetryQueueLeaseInvocation :execrows
UPDATE invocations SET state=sqlc.arg(state)::text, outcome=sqlc.narg(outcome)::text, last_error=sqlc.arg(last_error)::text,
    completed_at=sqlc.narg(completed_at)::timestamptz, due_at=sqlc.arg(due_at)::timestamptz,
    lease_expires_at=NULL, instance_id=NULL
WHERE id=sqlc.arg(id)::uuid AND state='dispatching' AND attempts=sqlc.arg(attempt)::integer
    AND replay_generation=sqlc.arg(replay_generation)::bigint AND lease_expires_at>clock_timestamp();

-- name: QueueLeasesByInstance :many
SELECT instance_id::text AS instance_id, count(*)::bigint AS leases FROM invocations
WHERE app_id=sqlc.arg(app_id)::uuid AND source='queue' AND state='dispatching'
    AND lease_expires_at>clock_timestamp() AND instance_id IS NOT NULL
GROUP BY instance_id;
