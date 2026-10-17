-- name: GetCIAutomationRecoveryEvent :one
SELECT * FROM ci_automation_events WHERE account_id=$1 AND binding_id=$2 AND id=$3;

-- name: TryLockCIAutomationRecoveryEvent :one
SELECT * FROM ci_automation_events WHERE account_id=$1 AND binding_id=$2 AND id=$3 FOR UPDATE SKIP LOCKED;

-- name: GetCIAutomationRetryByKey :one
SELECT * FROM ci_automation_event_retries WHERE account_id=$1 AND key_digest=$2;

-- name: CIAutomationEventHasExecution :one
SELECT EXISTS(SELECT 1 FROM ci_executions WHERE account_id=$1 AND idempotency_key='github-auto:' || sqlc.arg(event_id)::text);

-- name: InsertCIAutomationRetry :one
INSERT INTO ci_automation_event_retries(id,account_id,event_id,generation,key_digest,previous_attempts,previous_error)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- The receipt and this reset commit in one apid transaction. No execution is
-- inserted here, and stale worker lease tokens are never carried forward.
-- name: ResetCIAutomationEventForRetry :execrows
UPDATE ci_automation_events SET status='pending',attempts=0,last_error='',lease_token=NULL,lease_until=NULL,
 next_attempt_at=clock_timestamp(),retry_generation=retry_generation+1
WHERE account_id=$1 AND binding_id=$2 AND id=$3 AND retry_generation=$4 AND status='dead'
 AND attempts>=sqlc.arg(max_attempts)::integer AND last_error IN ('unavailable','capacity')
 AND execution_id IS NULL AND lease_token IS NULL AND lease_until IS NULL;
