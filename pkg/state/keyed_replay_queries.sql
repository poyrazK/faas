-- name: KeyedReplayParent :one
SELECT i.* FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
WHERE i.id=sqlc.arg(id)::uuid AND i.account_id=sqlc.arg(account_id)::uuid
AND i.environment_id IS NULL AND NOT EXISTS (SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=i.id)
FOR UPDATE OF i FOR SHARE OF a;

-- name: KeyedReplayLaneIdentity :one
SELECT i.app_id, i.work_policy_name, i.work_key_digest
FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
WHERE i.id=sqlc.arg(id)::uuid AND i.account_id=sqlc.arg(account_id)::uuid;

-- name: KeyedReplayLockLane :one
SELECT next_sequence FROM invocation_work_lanes
WHERE app_id=sqlc.arg(app_id)::uuid AND policy_name=sqlc.arg(policy_name)::text
  AND key_digest=sqlc.arg(key_digest)::bytea FOR UPDATE;

-- name: KeyedReplayAdvanceLane :exec
UPDATE invocation_work_lanes SET next_sequence=next_sequence+1
WHERE app_id=sqlc.arg(app_id)::uuid AND policy_name=sqlc.arg(policy_name)::text
  AND key_digest=sqlc.arg(key_digest)::bytea;

-- name: KeyedReplayChildID :one
SELECT replay_invocation_id FROM invocation_keyed_replays WHERE parent_invocation_id=$1;

-- name: KeyedReplayChild :one
SELECT * FROM invocations WHERE id=$1;

-- name: KeyedReplayRecordChild :exec
INSERT INTO invocation_keyed_replays (parent_invocation_id, replay_invocation_id) VALUES ($1, $2);

-- name: KeyedReplayExpired :one
SELECT (work_expires_at IS NOT NULL AND work_expires_at<=clock_timestamp())
  OR (start_deadline_at IS NOT NULL AND start_deadline_at<=clock_timestamp()) AS expired
FROM invocations WHERE id=$1;

-- name: KeyedWorkLaneHead :one
-- In-place replay can restore an older sequence while a later row owns the
-- lane. Ownership wins over pending FIFO; an expired broker owner can still
-- reclaim its own generation before the replay proceeds.
SELECT id, state, due FROM (
  SELECT id::text, state, due_at<=clock_timestamp() AS due, work_sequence
  FROM invocations
  WHERE app_id=sqlc.arg(app_id)::uuid AND work_policy_name=sqlc.arg(policy_name)::text
    AND work_key_digest=sqlc.arg(key_digest)::bytea AND state IN ('pending','dispatching')
  UNION ALL
  SELECT r.id::text, r.state, true AS due, r.work_sequence
  FROM trigger_records r JOIN triggers t ON t.id=r.trigger_id
  WHERE t.app_id=sqlc.arg(app_id)::uuid AND r.work_policy_name=sqlc.arg(policy_name)::text
    AND r.work_key_digest=sqlc.arg(key_digest)::bytea AND r.state IN ('pending','retry','claimed')
) work ORDER BY (state IN ('dispatching','claimed')) DESC, work_sequence LIMIT 1;

-- name: LockInvocationReplayLane :exec
SELECT l.app_id FROM invocation_work_lanes l JOIN invocations i
  ON l.app_id=i.app_id AND l.policy_name=i.work_policy_name AND l.key_digest=i.work_key_digest
WHERE i.id=sqlc.arg(id)::uuid AND i.account_id=sqlc.arg(account_id)::uuid
  AND (sqlc.narg(expected_app_id)::uuid IS NULL OR i.app_id=sqlc.narg(expected_app_id)::uuid)
FOR UPDATE OF l;

-- name: LockTriggerReplayLane :exec
SELECT l.app_id FROM invocation_work_lanes l JOIN trigger_records r
  ON l.policy_name=r.work_policy_name AND l.key_digest=r.work_key_digest
JOIN triggers t ON t.id=r.trigger_id AND t.app_id=l.app_id
WHERE r.id=sqlc.arg(id)::uuid
  AND (sqlc.narg(expected_account_id)::uuid IS NULL OR t.account_id=sqlc.narg(expected_account_id)::uuid)
  AND (sqlc.narg(expected_app_id)::uuid IS NULL OR t.app_id=sqlc.narg(expected_app_id)::uuid)
FOR UPDATE OF l;

-- name: LockDeadLetterReplayLanes :exec
-- Candidate selection holds no ledger locks. Take target lanes and source
-- rows before locking the projection, matching failure writers' lock order.
SELECT l.app_id FROM invocation_work_lanes l
WHERE (l.app_id,l.policy_name,l.key_digest) IN (
  SELECT i.app_id,i.work_policy_name,i.work_key_digest
  FROM dead_letter_events e JOIN invocations i ON e.source='invocation' AND i.id=e.source_id
    AND i.account_id=e.account_id AND i.app_id=e.app_id
  WHERE e.id=ANY(sqlc.arg(event_ids)::uuid[])
  UNION
  SELECT t.app_id,r.work_policy_name,r.work_key_digest
  FROM dead_letter_events e JOIN trigger_records r ON e.source='trigger_record' AND r.id=e.source_id
  JOIN triggers t ON t.id=r.trigger_id AND t.account_id=e.account_id AND t.app_id=e.app_id
  WHERE e.id=ANY(sqlc.arg(event_ids)::uuid[])
  UNION
  SELECT i.app_id,i.work_policy_name,i.work_key_digest
  FROM dead_letter_events e JOIN trigger_records r ON e.source='trigger_record' AND r.id=e.source_id
  JOIN triggers t ON t.id=r.trigger_id AND t.account_id=e.account_id AND t.app_id=e.app_id
  JOIN invocations i ON i.id::text=r.item_identifier AND i.app_id=t.app_id AND i.account_id=t.account_id
    AND i.source=t.source AND (i.queue_binding_id IS NULL OR i.queue_binding_id=t.queue_binding_id)
  WHERE e.id=ANY(sqlc.arg(event_ids)::uuid[]) AND t.kind='queue' AND t.source IN ('queue','delayed_task')
) ORDER BY l.app_id,l.policy_name,l.key_digest FOR UPDATE OF l;

-- name: DeadLetterReplayCandidateIDs :many
SELECT id::text FROM production_dead_letter_events
WHERE account_id=sqlc.arg(account_id)::uuid AND replayed_at IS NULL
  AND (sqlc.narg(expected_app_id)::uuid IS NULL OR app_id=sqlc.narg(expected_app_id)::uuid)
  AND (sqlc.narg(expected_event_id)::uuid IS NULL OR id=sqlc.narg(expected_event_id)::uuid)
ORDER BY last_failed_at DESC,id DESC LIMIT sqlc.arg(candidate_limit)::integer;

-- name: LockKeyedDeadLetterInvocationRows :exec
SELECT i.id FROM invocations i WHERE i.work_policy_name IS NOT NULL AND i.id IN (
  SELECT src.id FROM dead_letter_events e JOIN invocations src
    ON e.source='invocation' AND e.source_id=src.id AND e.account_id=src.account_id AND e.app_id=src.app_id
  WHERE e.source='invocation'
    AND e.id=ANY(sqlc.arg(event_ids)::uuid[])
  UNION
  SELECT inv.id FROM dead_letter_events e JOIN trigger_records r ON e.source='trigger_record' AND r.id=e.source_id
  JOIN triggers t ON t.id=r.trigger_id AND t.account_id=e.account_id AND t.app_id=e.app_id
  JOIN invocations inv ON inv.id::text=r.item_identifier AND inv.app_id=t.app_id AND inv.account_id=t.account_id
    AND inv.source=t.source AND (inv.queue_binding_id IS NULL OR inv.queue_binding_id=t.queue_binding_id)
  WHERE t.kind='queue' AND t.source IN ('queue','delayed_task') AND e.id=ANY(sqlc.arg(event_ids)::uuid[])
) ORDER BY i.id FOR UPDATE OF i;

-- name: LockKeyedDeadLetterTriggerRows :exec
SELECT r.id FROM trigger_records r JOIN triggers t ON t.id=r.trigger_id
WHERE r.id IN (SELECT src.id FROM dead_letter_events e JOIN trigger_records src ON e.source='trigger_record' AND src.id=e.source_id
  JOIN triggers owner ON owner.id=src.trigger_id AND owner.account_id=e.account_id AND owner.app_id=e.app_id
  WHERE e.id=ANY(sqlc.arg(event_ids)::uuid[]))
AND (r.work_policy_name IS NOT NULL OR (t.kind='queue' AND t.source IN ('queue','delayed_task')
  AND EXISTS (SELECT 1 FROM invocations i WHERE i.id::text=r.item_identifier
    AND i.app_id=t.app_id AND i.account_id=t.account_id AND i.source=t.source AND i.work_policy_name IS NOT NULL
    AND (i.queue_binding_id IS NULL OR i.queue_binding_id=t.queue_binding_id))))
ORDER BY r.id FOR UPDATE OF r;
