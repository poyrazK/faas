-- name: LockInvocationAttemptClaim :one
-- Check the live lease after acquiring the row lock, including lock waits.
WITH claim AS MATERIALIZED (
  SELECT id,state,attempts,replay_generation,lease_expires_at FROM invocations
  WHERE id=sqlc.arg(id)::uuid FOR UPDATE
)
SELECT id FROM claim WHERE state='dispatching' AND attempts=sqlc.arg(attempt)::integer
  AND replay_generation=sqlc.arg(replay_generation)::bigint
  AND lease_expires_at>clock_timestamp();

-- name: EventReceiptAttemptHistory :many
SELECT h.* FROM invocation_attempt_history h JOIN apps a ON a.id=h.app_id AND a.account_id=h.account_id
WHERE h.account_id=sqlc.arg(account_id)::uuid AND h.app_id=sqlc.arg(app_id)::uuid
  AND h.root_invocation_id=sqlc.arg(root_invocation_id)::uuid
  AND h.root_created_at>=sqlc.arg(accepted_at)::timestamptz
  AND (sqlc.arg(after_id)::bigint=0 OR h.id<sqlc.arg(after_id)::bigint)
ORDER BY h.id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: PruneInvocationAttemptHistory :execrows
DELETE FROM invocation_attempt_history WHERE id IN (
  SELECT id FROM invocation_attempt_history WHERE outcome<>'running' AND retain_until<=sqlc.arg(now_at)::timestamptz
  ORDER BY retain_until,id FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
);
