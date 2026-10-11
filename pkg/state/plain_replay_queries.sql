-- name: PlainReplayParent :one
SELECT i.* FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
WHERE i.id=sqlc.arg(id)::uuid AND i.account_id=sqlc.arg(account_id)::uuid
AND i.environment_id IS NULL AND NOT EXISTS (SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=i.id)
FOR UPDATE OF i FOR SHARE OF a;

-- name: PlainReplayIdentity :one
SELECT replay_invocation_id, replay_created_at FROM invocation_plain_replays WHERE parent_invocation_id=$1;

-- name: PlainReplayRecordChild :exec
INSERT INTO invocation_plain_replays (parent_invocation_id, replay_invocation_id, replay_created_at) VALUES ($1, $2, $3);
