-- name: InsertCIExecutionCheck :exec
INSERT INTO ci_execution_checks(account_id,execution_id) VALUES($1,$2);

-- Expiry is bounded and skips publishers holding a delivery transaction.
-- name: ExpireCIExecutionChecks :exec
WITH expired AS (
 SELECT c.execution_id FROM ci_execution_checks c WHERE c.account_id=$1 AND c.status='sending'
 AND c.attempts>=sqlc.arg(max_attempts)::integer AND c.lease_until<=clock_timestamp()
 FOR UPDATE SKIP LOCKED
)
UPDATE ci_execution_checks c SET status='dead',lease_token=NULL,lease_until=NULL,last_error='attempts_exhausted'
FROM expired x WHERE c.execution_id=x.execution_id;

-- Execution versions are the durable desired state. No publication locks are
-- taken by execution writers, so a slow provider cannot block cancellation.
-- name: ClaimCIExecutionCheck :one
WITH candidate AS (
 SELECT c.execution_id FROM ci_execution_checks c JOIN ci_executions e ON e.id=c.execution_id
 WHERE c.account_id=$1 AND e.archived_at IS NULL AND c.attempts<sqlc.arg(max_attempts)::integer AND
 ((c.status='pending' AND c.next_attempt_at<=clock_timestamp())
 OR (c.status='sending' AND c.lease_until<=clock_timestamp())
 OR (c.status='sent' AND e.version>c.delivered_version))
 ORDER BY c.next_attempt_at,c.created_at,c.execution_id FOR UPDATE OF c SKIP LOCKED LIMIT 1
)
UPDATE ci_execution_checks c SET status='sending',attempts=attempts+1,lease_token=sqlc.arg(token)::uuid,
 lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
FROM candidate x WHERE c.execution_id=x.execution_id RETURNING c.*;

-- Held across the bounded GitHub RPC. Another publisher skips this row even
-- after lease expiry; late local claims cannot write an older projection.
-- name: LockCIExecutionCheck :one
SELECT * FROM ci_execution_checks WHERE account_id=$1 AND execution_id=$2 FOR UPDATE;

-- name: FinishCIExecutionCheck :execrows
UPDATE ci_execution_checks SET status='sent',attempts=0,lease_token=NULL,lease_until=NULL,
 delivered_version=$4,delivered_digest=$5,check_id=$6,last_error='',next_attempt_at=clock_timestamp()
WHERE account_id=$1 AND execution_id=$2 AND status='sending' AND lease_token=$3
 AND lease_until>clock_timestamp() AND delivered_version<=$4;

-- name: FailCIExecutionCheck :execrows
UPDATE ci_execution_checks SET status=$4,last_error=$5,next_attempt_at=$6,lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND execution_id=$2 AND status='sending' AND lease_token=$3 AND lease_until>clock_timestamp();
