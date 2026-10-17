-- name: LockCIDispatchBudget :one
SELECT lock_id FROM ci_dispatch_budget WHERE lock_id=true FOR UPDATE;

-- name: CountCIActiveExecutions :one
SELECT count(*)::bigint AS global_count,
 count(*) FILTER (WHERE account_id=sqlc.arg(account_id)::uuid)::bigint AS account_count
FROM ci_executions WHERE mode='jobs' AND status IN ('queued','running','cancelling');

-- name: CountCIDispatchLeases :one
SELECT count(*) FROM ci_executions WHERE mode='jobs' AND lease_until > sqlc.arg(now_at)::timestamptz;

-- name: NextCIDispatchExecution :one
SELECT e.* FROM ci_executions e
WHERE e.mode='jobs' AND e.status IN ('queued','running','cancelling')
 AND e.next_reconcile_at <= sqlc.arg(now_at)::timestamptz
 AND (e.lease_until IS NULL OR e.lease_until <= sqlc.arg(now_at)::timestamptz)
 AND (SELECT count(*) FROM ci_executions a WHERE a.mode='jobs' AND a.account_id=e.account_id
      AND a.lease_until > sqlc.arg(now_at)::timestamptz) < sqlc.arg(account_leases)::integer
ORDER BY e.next_reconcile_at,e.id LIMIT 1 FOR UPDATE OF e SKIP LOCKED;

-- name: ScheduleCIDispatchClaim :exec
UPDATE ci_executions SET next_reconcile_at=$3 WHERE id=$1 AND fence=$2;

-- name: FinishCIDispatchSchedule :exec
UPDATE ci_executions SET reconcile_failures=CASE WHEN sqlc.arg(failed)::boolean THEN least(reconcile_failures+1,sqlc.arg(max_failures)::integer) ELSE 0 END,
 next_reconcile_at=clock_timestamp()+make_interval(secs => CASE WHEN sqlc.arg(failed)::boolean
 THEN least(sqlc.arg(max_backoff)::integer,sqlc.arg(poll_seconds)::integer*(1 << least(reconcile_failures,sqlc.arg(max_failures)::integer)))
 ELSE sqlc.arg(poll_seconds)::integer END)
WHERE id=$1 AND fence=$2 AND version=sqlc.arg(expected_version)::bigint;

-- name: ListCIJobsExecutions :many
SELECT id,revision_id,commit_sha,status,created_at,finished_at,rerun_parent_id,rerun_root_id,(snapshot->>'pipeline')::text AS pipeline
FROM ci_executions WHERE account_id=$1 AND mode='jobs' AND archived_at IS NULL
ORDER BY created_at DESC,id LIMIT sqlc.arg(max_rows)::integer;
