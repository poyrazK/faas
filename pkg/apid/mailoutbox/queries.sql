-- name: Ready :exec
SELECT id,recovery_id,template_version,recipient_address,revision FROM platform_mail_producer_outbox LIMIT 0;

-- Match maild's recovery fence without retaining it across HTTP. Claims take
-- this before outbox rows; scheduling takes admission/account locks first so
-- a waiting deletion cannot obstruct a handoff's maild admission. Dispatch
-- sends a generation header that maild validates under its own transaction.
-- name: ShareRecovery :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended($1::text,871739));

-- name: Recovery :one
SELECT coalesce((SELECT r.recovery_id FROM internal_mail_recovery_state r WHERE r.namespace=sqlc.arg(namespace)),
    '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS recovery_id,
    EXISTS(SELECT 1 FROM internal_mail_recovery_state r WHERE r.namespace=sqlc.arg(namespace) AND r.held) AS held;

-- name: LockAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text,871740));

-- name: Pending :one
SELECT count(*)::bigint FROM platform_mail_producer_outbox WHERE namespace=$1 AND state IN ('queued','claimed');

-- name: LockAccount :one
SELECT id,email,status,deletion_requested_at FROM accounts WHERE id=$1 FOR UPDATE;

-- name: ShareAccount :one
SELECT id,email,status,deletion_requested_at,email_verified_at FROM accounts WHERE id=$1 FOR SHARE;

-- name: ScheduleDeletion :one
UPDATE accounts SET status='deleted_pending',deletion_requested_at=coalesce(deletion_requested_at,clock_timestamp())
WHERE id=$1 AND status IN ('active','deleted_pending') RETURNING id,email,deletion_requested_at;

-- name: ExistingIntent :one
SELECT id FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='account_deletion_pending' AND deletion_requested_at=$2;

-- name: CreateIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,deletion_requested_at,restore_until,expires_at,recovery_id)
VALUES($1,$2,$3,$4,$5,$6,'account_deletion_pending',1,$7,
    $7::timestamptz+sqlc.arg(grace_seconds)::integer*interval '1 second',
    $7::timestamptz+sqlc.arg(ttl_seconds)::integer*interval '1 second',$8);

-- Bounded maintenance preserves live leases. Recovery-held/mismatched jobs
-- may expire, but are never resubmitted merely because a global hold releases.
-- name: Maintain :exec
WITH candidates AS (
    SELECT pending.id FROM platform_mail_producer_outbox pending WHERE pending.namespace=sqlc.arg(namespace)
      AND (pending.state='queued' OR (pending.state='claimed' AND pending.lease_until<=clock_timestamp()))
      AND (pending.expires_at<=clock_timestamp() OR pending.attempts>=sqlc.arg(max_attempts))
    ORDER BY pending.id LIMIT sqlc.arg(batch_size) FOR UPDATE SKIP LOCKED
)
UPDATE platform_mail_producer_outbox o SET state=CASE WHEN expires_at<=clock_timestamp() THEN 'expired' ELSE 'failed' END,
    code=CASE WHEN expires_at<=clock_timestamp() THEN 'notification_expired' ELSE 'attempt_limit' END,
    recipient_address='',lease_id=NULL,lease_until=NULL,verification_token_hash=NULL,sealed_verification='',verification_key_id='',verification_origin='',updated_at=clock_timestamp(),revision=o.revision+1
FROM candidates c WHERE o.id=c.id;

-- name: Claim :one
WITH candidate AS (
    SELECT id FROM platform_mail_producer_outbox o WHERE o.namespace=sqlc.arg(namespace)
      AND o.producer_id=sqlc.arg(producer_id) AND o.from_address=sqlc.arg(from_address)
      AND (o.template='account_deletion_pending' OR (sqlc.arg(verification_enabled)::boolean AND o.verification_origin=sqlc.arg(verification_origin)::text
          AND o.verification_key_id=ANY(sqlc.arg(verification_keys)::text[])))
      AND o.attempts<sqlc.arg(max_attempts) AND o.expires_at>clock_timestamp()
      AND ((o.state='queued' AND o.next_attempt_at<=clock_timestamp()) OR (o.state='claimed' AND o.lease_until<=clock_timestamp()))
      AND NOT EXISTS(SELECT 1 FROM internal_mail_recovery_state s WHERE s.namespace=o.namespace AND (s.held OR s.recovery_id<>o.recovery_id))
    ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE platform_mail_producer_outbox o SET state='claimed',attempts=attempts+1,lease_id=sqlc.arg(lease_id),
    lease_until=clock_timestamp()+sqlc.arg(lease_seconds)::integer*interval '1 second',updated_at=clock_timestamp(),revision=o.revision+1
FROM candidate c WHERE o.id=c.id RETURNING o.*;

-- name: Intent :one
SELECT * FROM platform_mail_producer_outbox WHERE namespace=$1 AND id=$2;

-- name: LockIntent :one
SELECT * FROM platform_mail_producer_outbox WHERE namespace=$1 AND id=$2 FOR UPDATE;

-- name: LiveLease :one
SELECT EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE namespace=$1 AND id=$2 AND lease_id=$3
    AND state='claimed' AND lease_until>clock_timestamp());

-- name: Suppressed :one
SELECT EXISTS(SELECT 1 FROM mail_suppressions WHERE lower(email)=lower($1::text) AND (expires_at IS NULL OR expires_at>clock_timestamp()));

-- name: Settle :execrows
UPDATE platform_mail_producer_outbox SET state=$4,code=$5,lease_id=NULL,lease_until=NULL,
    attempts=CASE WHEN sqlc.arg(refund_attempt)::boolean THEN greatest(0,attempts-1) ELSE attempts END,
    recipient_address=CASE WHEN $4='queued' THEN recipient_address ELSE '' END,
    verification_token_hash=CASE WHEN $4='queued' THEN verification_token_hash ELSE NULL END,
    sealed_verification=CASE WHEN $4='queued' THEN sealed_verification ELSE '' END,
    verification_key_id=CASE WHEN $4='queued' THEN verification_key_id ELSE '' END,
    verification_origin=CASE WHEN $4='queued' THEN verification_origin ELSE '' END,
    next_attempt_at=clock_timestamp()+sqlc.arg(retry_seconds)::integer*interval '1 second',updated_at=clock_timestamp(),revision=revision+1
WHERE namespace=$1 AND id=$2 AND lease_id=$3 AND state='claimed';

-- maild uses metadata reads only. It never owns outbox mutations.
-- name: OperatorSummary :many
SELECT o.state,count(*)::bigint AS total,
    count(*) FILTER (WHERE o.state IN ('queued','claimed') AND EXISTS(SELECT 1 FROM internal_mail_recovery_state s
        WHERE s.namespace=o.namespace AND (s.held OR s.recovery_id<>o.recovery_id)))::bigint AS recovery_held
FROM platform_mail_producer_outbox o WHERE o.namespace=$1 GROUP BY o.state ORDER BY o.state;

-- name: OperatorItems :many
SELECT o.id,o.account_id,o.producer_id,o.template,o.template_version,o.state,o.attempts,o.code,o.revision,
    o.created_at,o.updated_at,o.expires_at,o.next_attempt_at,o.lease_until,
    EXISTS(SELECT 1 FROM internal_mail_recovery_state s WHERE s.namespace=o.namespace AND o.state IN ('queued','claimed') AND (s.held OR s.recovery_id<>o.recovery_id)) AS recovery_held
FROM platform_mail_producer_outbox o WHERE o.namespace=sqlc.arg(namespace) AND o.id>sqlc.arg(after_id)
    AND (sqlc.arg(status)::text='' OR o.state=sqlc.arg(status)) ORDER BY o.id LIMIT sqlc.arg(page_limit);

-- name: OperatorItem :one
SELECT o.id,o.account_id,o.producer_id,o.template,o.template_version,o.state,o.attempts,o.code,o.revision,
    o.created_at,o.updated_at,o.expires_at,o.next_attempt_at,o.lease_until,
    EXISTS(SELECT 1 FROM internal_mail_recovery_state s WHERE s.namespace=o.namespace AND o.state IN ('queued','claimed') AND (s.held OR s.recovery_id<>o.recovery_id)) AS recovery_held
FROM platform_mail_producer_outbox o WHERE o.namespace=$1 AND o.id=$2;

-- Metadata-only exact producer/key lookup. A missing admission says nothing
-- about external custody after a database restore and never authorizes replay.
-- name: AdmissionLookup :one
SELECT o.id,o.revision,o.state,o.template,o.from_address,o.expires_at,o.recovery_id AS intent_recovery_id,
    (o.state='claimed' AND o.lease_until>clock_timestamp()) AS active_claim,
    coalesce((SELECT s.recovery_id FROM internal_mail_recovery_state s WHERE s.namespace=o.namespace),
        '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS recovery_id,
    EXISTS(SELECT 1 FROM internal_mail_recovery_state s WHERE s.namespace=o.namespace AND s.held) AS recovery_held,
    m.id AS message_id,m.created_at AS admitted_at,
    (m.sender_address IS NOT NULL AND m.created_at>=o.created_at AND m.send_at IS NULL AND m.expires_at=o.expires_at
        AND (SELECT count(*) FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)=1) AS metadata_matches,
    coalesce(m.sender_address,'')::text AS sender_address
FROM platform_mail_producer_outbox o
LEFT JOIN internal_mail_messages m ON m.namespace=o.namespace AND m.producer_id=o.producer_id
    AND m.idempotency_key=(CASE WHEN o.template='email_verification' THEN 'verify-email:' ELSE 'account-delete:' END)||o.id::text
WHERE o.namespace=$1 AND o.id=$2;

-- name: ReconciliationReady :exec
SELECT request_id,request_hash FROM platform_mail_outbox_reconciliations LIMIT 0;

-- This fence is independent of maild's operation audit and of dispatch. Never
-- acquire recovery before waiting for account/intent locks in this path.
-- name: LockReconciliation :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text||':'||sqlc.arg(request_id)::text,871741));

-- name: Reconciliation :one
SELECT * FROM platform_mail_outbox_reconciliations WHERE namespace=$1 AND request_id=$2;

-- name: AcknowledgeAdmission :execrows
UPDATE platform_mail_producer_outbox SET state='admitted',code='',recipient_address='',lease_id=NULL,lease_until=NULL,
    verification_token_hash=NULL,sealed_verification='',verification_key_id='',verification_origin='',
    updated_at=clock_timestamp(),revision=revision+1
WHERE namespace=$1 AND id=$2 AND revision=$3 AND state IN ('queued','claimed','failed','expired')
    AND (state<>'claimed' OR lease_until<=clock_timestamp());

-- name: InsertReconciliation :one
INSERT INTO platform_mail_outbox_reconciliations(namespace,request_id,intent_id,message_id,recovery_id,expected_revision,
    resulting_revision,previous_state,actor,reason,evidence,request_hash,template)
VALUES(sqlc.arg(namespace),sqlc.arg(request_id),sqlc.arg(intent_id),sqlc.arg(message_id),sqlc.arg(recovery_id),
    sqlc.arg(expected_revision)::bigint,sqlc.arg(expected_revision)::bigint+1,sqlc.arg(previous_state),
    sqlc.arg(actor),sqlc.arg(reason),sqlc.arg(evidence),sqlc.arg(request_hash),sqlc.arg(template)) RETURNING *;

-- name: LockVerificationAccount :one
SELECT id,email,status,email_verified_at FROM accounts WHERE id=$1 FOR UPDATE;

-- name: VerificationClock :one
SELECT clock_timestamp()::timestamptz AS issued_at;

-- name: CreateVerificationToken :exec
INSERT INTO email_verification_tokens(token_hash,account_id,expires_at,email_address) VALUES($1,$2,$3,$4);

-- name: CreateVerificationIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,verification_token_hash,sealed_verification,verification_key_id,verification_origin)
VALUES($1,$2,$3,$4,$5,$6,'email_verification',1,$7,$8,$9,$10,$11,$12);

-- Consumption locks account before token, matching dispatch and account erasure.
-- name: ShareVerificationToken :one
SELECT token_hash,account_id,expires_at,consumed_at,email_address FROM email_verification_tokens
WHERE token_hash=$1 AND account_id=$2 FOR SHARE;

-- name: VerificationKeyReferences :many
SELECT DISTINCT verification_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='email_verification' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();
