-- name: RecoverySchema :exec
SELECT s.recovery_id,h.recovery_id,r.recovery_id FROM internal_mail_recovery_state s,
    internal_mail_recovery_runs h,internal_mail_recipients r LIMIT 0;

-- Recovery (871739) precedes encryption, address, producer, domain and row
-- fences. Restoring requires all maild instances stopped; this additionally
-- serializes admission/handoff with a newly configured recovery generation.
-- name: ShareRecovery :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended($1::text,871739));

-- name: LockRecovery :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text,871739));

-- name: RecoveryState :one
SELECT * FROM internal_mail_recovery_state WHERE namespace=$1;

-- name: RecoverySeen :one
SELECT EXISTS(SELECT 1 FROM internal_mail_recovery_runs WHERE namespace=$1 AND recovery_id=$2);

-- name: InsertRecoveryRun :exec
INSERT INTO internal_mail_recovery_runs(namespace,recovery_id) VALUES($1,$2);

-- name: StartRecovery :one
INSERT INTO internal_mail_recovery_state(namespace,recovery_id,revision,held) VALUES($1,$2,1,true)
ON CONFLICT(namespace) DO UPDATE SET recovery_id=excluded.recovery_id,
    revision=internal_mail_recovery_state.revision+1,held=true,updated_at=clock_timestamp()
RETURNING *;

-- name: ReleaseRecovery :one
UPDATE internal_mail_recovery_state SET held=false,revision=revision+1,updated_at=clock_timestamp()
WHERE namespace=$1 RETURNING *;

-- name: RecoveryPermitted :one
SELECT NOT EXISTS(SELECT 1 FROM internal_mail_recovery_state WHERE namespace=$1 AND held);

-- name: RecipientRecoveryPermitted :one
SELECT NOT EXISTS(SELECT 1 FROM internal_mail_recovery_state s WHERE s.namespace=r.namespace
    AND (s.held OR s.recovery_id<>r.recovery_id))::boolean AS allowed
FROM internal_mail_recipients r WHERE r.namespace=$1 AND r.id=$2;

-- name: RecoveryStatistics :one
SELECT count(*) FILTER (WHERE r.recovery_id<>s.recovery_id
        AND r.status IN ('queued','deferred','claimed','submitting','uncertain','submitted'))::bigint AS quarantined,
    count(*) FILTER (WHERE r.status='submitting' AND r.lease_until>clock_timestamp())::bigint AS active_submissions
FROM internal_mail_recovery_state s JOIN internal_mail_recipients r ON r.namespace=s.namespace
WHERE s.namespace=$1;

-- name: RecoveryRecipients :many
SELECT r.id,r.message_id,r.status,r.lease_until FROM internal_mail_recipients r
JOIN internal_mail_recovery_state s ON s.namespace=r.namespace
WHERE r.namespace=sqlc.arg(namespace) AND r.recovery_id<>s.recovery_id
    AND r.status IN ('queued','deferred','claimed','submitting','uncertain','submitted')
    AND r.id>sqlc.arg(after_id) ORDER BY r.id LIMIT sqlc.arg(page_limit);

-- name: ReviewRecoveryRecipient :execrows
UPDATE internal_mail_recipients SET recovery_id=$3,status=$4,code=$5,
    lease_id=NULL,lease_until=NULL,next_attempt_at=greatest(next_attempt_at,clock_timestamp()),updated_at=clock_timestamp()
WHERE namespace=$1 AND id=$2;

-- name: RecoveryRecipientActive :one
SELECT coalesce(lease_until>clock_timestamp(),false)::boolean AS active
FROM internal_mail_recipients WHERE namespace=$1 AND id=$2;
