-- name: KeyedReplayParent :one
SELECT i.* FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
WHERE i.id=sqlc.arg(id)::uuid AND i.account_id=sqlc.arg(account_id)::uuid
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
