-- name: Ready :exec
SELECT o.id,o.recovery_id,o.template_version,o.recipient_address,o.revision,o.sealed_security_alert,o.security_alert_key_id,a.mfa_enrolled_at,a.mfa_recovery_codes_hash,a.password_reset_revision,t.password_revision,m.password_revision,d.mfa_revision,a.mfa_revision
FROM platform_mail_producer_outbox o LEFT JOIN accounts a ON a.id=o.account_id
LEFT JOIN password_reset_tokens t ON t.account_id=a.id
LEFT JOIN magic_link_tokens m ON m.account_id=a.id
LEFT JOIN mfa_disable_tokens d ON d.account_id=a.id LIMIT 0;

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
SELECT id,email,status,deletion_requested_at,email_verified_at,abuse_hold_at,password_reset_revision,mfa_enrolled_at,mfa_revision,past_due_at,billing_mail_revision FROM accounts WHERE id=$1 FOR SHARE;

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
    recipient_address='',lease_id=NULL,lease_until=NULL,verification_token_hash=NULL,sealed_verification='',verification_key_id='',verification_origin='',sealed_security_alert='',security_alert_key_id='',updated_at=clock_timestamp(),revision=o.revision+1
FROM candidates c WHERE o.id=c.id;

-- name: Claim :one
WITH candidate AS (
    SELECT id FROM platform_mail_producer_outbox o WHERE o.namespace=sqlc.arg(namespace)
      AND o.producer_id=sqlc.arg(producer_id) AND o.from_address=sqlc.arg(from_address)
      AND (o.template='account_deletion_pending' OR (o.template='email_verification' AND sqlc.arg(verification_enabled)::boolean AND o.verification_origin=sqlc.arg(verification_origin)::text
          AND o.verification_key_id=ANY(sqlc.arg(verification_keys)::text[]))
        OR (o.template='password_reset' AND sqlc.arg(password_reset_enabled)::boolean AND o.verification_origin=sqlc.arg(password_reset_origin)::text
          AND o.verification_key_id=ANY(sqlc.arg(password_reset_keys)::text[]))
        OR (o.template='magic_link' AND sqlc.arg(magic_link_enabled)::boolean AND o.verification_origin=sqlc.arg(magic_link_origin)::text
          AND o.verification_key_id=ANY(sqlc.arg(magic_link_keys)::text[]))
        OR (o.template='mfa_disable_request' AND sqlc.arg(mfa_disable_enabled)::boolean AND o.verification_origin=sqlc.arg(mfa_disable_origin)::text
          AND o.verification_key_id=ANY(sqlc.arg(mfa_disable_keys)::text[]))
        OR (o.template='nonpayment_deletion' AND sqlc.arg(nonpayment_deletion_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(nonpayment_deletion_keys)::text[]))
        OR (o.template='account_suspended' AND sqlc.arg(account_suspended_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(account_suspended_keys)::text[]))
        OR (o.template='account_restored' AND sqlc.arg(account_restored_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(account_restored_keys)::text[]))
        OR (o.template='payment_failed' AND sqlc.arg(payment_failed_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(payment_failed_keys)::text[]))
        OR (o.template='mfa_enabled' AND sqlc.arg(mfa_enabled_alert_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(mfa_enabled_alert_keys)::text[]))
        OR (o.template='password_changed' AND sqlc.arg(password_changed_alert_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(password_changed_alert_keys)::text[]))
        OR (o.template='mfa_disabled' AND sqlc.arg(mfa_disabled_alert_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(mfa_disabled_alert_keys)::text[]))
        OR (o.template='mfa_recovery_used' AND sqlc.arg(mfa_recovery_alert_enabled)::boolean
          AND o.security_alert_key_id=ANY(sqlc.arg(mfa_recovery_alert_keys)::text[])))
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
    sealed_security_alert=CASE WHEN $4='queued' THEN sealed_security_alert ELSE '' END,
    security_alert_key_id=CASE WHEN $4='queued' THEN security_alert_key_id ELSE '' END,
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
    AND m.idempotency_key=(CASE o.template WHEN 'email_verification' THEN 'verify-email:' WHEN 'password_reset' THEN 'password-reset:' WHEN 'magic_link' THEN 'magic-link:' WHEN 'mfa_recovery_used' THEN 'mfa-recovery-alert:' WHEN 'nonpayment_deletion' THEN 'nonpayment-deletion:' WHEN 'account_suspended' THEN 'account-suspended:' WHEN 'account_restored' THEN 'account-restored:' WHEN 'payment_failed' THEN 'payment-failed:' WHEN 'mfa_enabled' THEN 'mfa-enabled:' WHEN 'password_changed' THEN 'password-changed:' WHEN 'mfa_disabled' THEN 'mfa-disabled:' WHEN 'mfa_disable_request' THEN 'mfa-disable:' ELSE 'account-delete:' END)||o.id::text
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
    verification_token_hash=NULL,sealed_verification='',verification_key_id='',verification_origin='',sealed_security_alert='',security_alert_key_id='',
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

-- name: LockPasswordResetAccount :one
SELECT id,email,status,email_verified_at,password_reset_revision FROM accounts WHERE id=$1 FOR UPDATE;

-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens(token_hash,account_id,email_address,password_revision,expires_at,namespace,recovery_id) VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: CreatePasswordResetIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,verification_token_hash,sealed_verification,verification_key_id,verification_origin)
VALUES($1,$2,$3,$4,$5,$6,'password_reset',1,$7,$8,$9,$10,$11,$12);

-- name: SharePasswordResetToken :one
SELECT * FROM password_reset_tokens WHERE token_hash=$1 AND account_id=$2 FOR SHARE;

-- name: PasswordResetKeyReferences :many
SELECT DISTINCT verification_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='password_reset' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- The account is the first row locked even when two different reset links race.
-- name: LockPasswordResetCredentialAccount :one
SELECT a.id,a.email,a.status,a.email_verified_at,a.password_reset_revision
FROM accounts a JOIN password_reset_tokens t ON t.account_id=a.id
WHERE t.token_hash=$1 FOR UPDATE OF a;

-- name: LockPasswordResetToken :one
SELECT * FROM password_reset_tokens WHERE token_hash=$1 AND account_id=$2 FOR UPDATE;

-- name: ReplaceResetPassword :execrows
WITH changed AS (
    UPDATE accounts SET password_reset_revision=password_reset_revision+1
    WHERE id=$1 AND password_reset_revision=$2 RETURNING id
)
INSERT INTO account_passwords(account_id,hash,updated_at)
SELECT id,$3,clock_timestamp() FROM changed
ON CONFLICT(account_id) DO UPDATE SET hash=excluded.hash,updated_at=excluded.updated_at;

-- name: ConsumePasswordResetToken :execrows
UPDATE password_reset_tokens SET consumed_at=clock_timestamp()
WHERE token_hash=$1 AND account_id=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp();

-- Hashes and address bindings have no purpose after expiry. Maintenance is
-- bounded and independent of the child gate; it never reissues credentials.
-- name: PurgeExpiredPasswordResetTokens :exec
WITH expired AS (
    SELECT token_hash FROM password_reset_tokens WHERE expires_at<=clock_timestamp()
    ORDER BY expires_at,token_hash LIMIT $1 FOR UPDATE SKIP LOCKED
)
DELETE FROM password_reset_tokens t USING expired e WHERE t.token_hash=e.token_hash;

-- name: LockMagicLinkAccount :one
SELECT id,email,status,email_verified_at,abuse_hold_at,password_reset_revision FROM accounts WHERE id=$1 FOR UPDATE;

-- name: CreateMagicLinkToken :exec
INSERT INTO magic_link_tokens(token_hash,account_id,email_address,password_revision,email_verified_at,expires_at,namespace,recovery_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: CreateMagicLinkIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,verification_token_hash,sealed_verification,verification_key_id,verification_origin)
VALUES($1,$2,$3,$4,$5,$6,'magic_link',1,$7,$8,$9,$10,$11,$12);

-- name: ShareMagicLinkToken :one
SELECT * FROM magic_link_tokens WHERE token_hash=$1 AND account_id=$2 FOR SHARE;

-- name: MagicLinkKeyReferences :many
SELECT DISTINCT verification_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='magic_link' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- Account-first locking matches dispatch, password writes and account erasure.
-- name: LockMagicLinkCredentialAccount :one
SELECT a.id,a.email,a.status,a.email_verified_at,a.abuse_hold_at,a.password_reset_revision
FROM accounts a JOIN magic_link_tokens t ON t.account_id=a.id
WHERE t.token_hash=$1 FOR UPDATE OF a;

-- name: LockMagicLinkToken :one
SELECT * FROM magic_link_tokens WHERE token_hash=$1 AND account_id=$2 FOR UPDATE;

-- These ownership-proof writes share the account lock and commit with token
-- consumption. The final CAS checks expiry after any dependent-row wait.
-- name: DeleteMagicLinkPassword :exec
DELETE FROM account_passwords WHERE account_id=$1;

-- name: RevokeMagicLinkSessions :execrows
UPDATE sessions SET revoked_at=clock_timestamp() WHERE account_id=$1 AND revoked_at IS NULL;

-- name: DeleteMagicLinkAPIKeys :execrows
DELETE FROM api_keys WHERE account_id=$1;

-- name: ClaimMagicLinkOwnership :exec
UPDATE accounts SET email_verified_at=clock_timestamp(),password_reset_revision=password_reset_revision+1 WHERE id=$1;

-- name: ConsumeMagicLinkToken :execrows
UPDATE magic_link_tokens SET consumed_at=clock_timestamp()
WHERE token_hash=$1 AND account_id=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp();

-- name: PurgeExpiredMagicLinkTokens :exec
WITH expired AS (
    SELECT token_hash FROM magic_link_tokens WHERE expires_at<=clock_timestamp()
    ORDER BY expires_at,token_hash LIMIT $1 FOR UPDATE SKIP LOCKED
)
DELETE FROM magic_link_tokens t USING expired e WHERE t.token_hash=e.token_hash;


-- Only apid owns recovery-code consumption and the paired notification intent.
-- No presented/stored recovery hash is copied into that intent.
-- name: LockMFARecoveryAlertAccount :one
SELECT id,email,status,abuse_hold_at,mfa_enrolled_at,mfa_recovery_codes_hash FROM accounts WHERE id=$1 FOR UPDATE;

-- name: ConsumeMFARecoveryAlertCode :exec
UPDATE accounts SET mfa_recovery_codes_hash=sqlc.arg(recovery_hashes)::bytea[] WHERE id=$1;

-- name: CreateMFARecoveryAlertIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id)
VALUES($1,$2,$3,$4,$5,$6,'mfa_recovery_used',1,$7,$8,$9,$10);

-- name: MFARecoveryAlertKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='mfa_recovery_used' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: LockMFADisableAccount :one
SELECT id,email,status,abuse_hold_at,password_reset_revision,mfa_revision,mfa_enrolled_at FROM accounts WHERE id=$1 FOR UPDATE;

-- Account lock always precedes pending token rows. This also invalidates old
-- inline requests; the flag-off writer follows the same order and revocation.
-- name: InvalidateMFADisableTokens :exec
UPDATE mfa_disable_tokens SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL;

-- name: InvalidateLegacyMFADisableRequests :exec
UPDATE mfa_disable_requests SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL;

-- name: CreateMFADisableToken :exec
INSERT INTO mfa_disable_tokens(token_hash,account_id,email_address,password_revision,mfa_revision,mfa_enrolled_at,namespace,recovery_id,requested_at,ready_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);

-- name: CreateMFADisableIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,verification_token_hash,sealed_verification,verification_key_id,verification_origin)
VALUES($1,$2,$3,$4,$5,$6,'mfa_disable_request',1,$7,$8,$9,$10,$11,$12);

-- name: ShareMFADisableToken :one
SELECT * FROM mfa_disable_tokens WHERE token_hash=$1 AND account_id=$2 FOR SHARE;

-- name: MFADisableKeyReferences :many
SELECT DISTINCT verification_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='mfa_disable_request' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: LockMFADisableCredentialAccount :one
SELECT a.id,a.email,a.status,a.abuse_hold_at,a.password_reset_revision,a.mfa_revision,a.mfa_enrolled_at
FROM accounts a JOIN mfa_disable_tokens t ON t.account_id=a.id WHERE t.token_hash=$1 FOR UPDATE OF a;

-- name: LockMFADisableToken :one
SELECT * FROM mfa_disable_tokens WHERE token_hash=$1 AND account_id=$2 FOR UPDATE;

-- These writes share the account lock; the final token CAS checks cooldown and
-- expiry after all waits and failure rolls back the MFA clear and revision.
-- name: ClearMFADisableFactor :exec
UPDATE accounts SET mfa_secret_encrypted=NULL,mfa_recovery_codes_hash=NULL,mfa_enrolled_at=NULL,mfa_revision=mfa_revision+1 WHERE id=$1;

-- name: ConsumeMFADisableToken :execrows
UPDATE mfa_disable_tokens SET consumed_at=clock_timestamp()
WHERE token_hash=$1 AND account_id=$2 AND consumed_at IS NULL AND ready_at<=clock_timestamp() AND expires_at>clock_timestamp();

-- name: PurgeExpiredMFADisableTokens :exec
WITH expired AS (
    SELECT token_hash FROM mfa_disable_tokens WHERE expires_at<=clock_timestamp()
    ORDER BY expires_at,token_hash LIMIT $1 FOR UPDATE SKIP LOCKED
)
DELETE FROM mfa_disable_tokens t USING expired e WHERE t.token_hash=e.token_hash;

-- name: MFADisabledAlertReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('mfa_disabled' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: MFADisabledAlertKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='mfa_disabled' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: CreateMFADisabledAlertIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id)
VALUES($1,$2,$3,$4,$5,$6,'mfa_disabled',1,$7,$8,$9,$10);

-- Read the password proof without holding the namespace/account locks during
-- expensive verification. Every password/MFA writer advances a locked revision.
-- name: MFADisabledPasswordProof :one
SELECT a.password_reset_revision,a.mfa_revision,p.hash FROM accounts a JOIN account_passwords p ON p.account_id=a.id WHERE a.id=$1;

-- name: LockMFADisabledAccount :one
SELECT id,email,status,abuse_hold_at,password_reset_revision,mfa_revision,mfa_enrolled_at,mfa_recovery_codes_hash FROM accounts WHERE id=$1 FOR UPDATE;

-- Legacy tokens have no expiry or saved password/factor version. Keep their
-- existing age-sealed proof and cooldown, and refuse pre-enrollment requests.
-- name: LockLegacyMFADisableRequest :one
SELECT token_hash,account_id,requested_at,consumed_at FROM mfa_disable_requests WHERE token_hash=$1 AND account_id=$2 FOR UPDATE;

-- name: ConsumeLegacyMFADisableRequest :execrows
UPDATE mfa_disable_requests SET consumed_at=clock_timestamp()
WHERE token_hash=$1 AND account_id=$2 AND consumed_at IS NULL AND requested_at+interval '24 hours'<=clock_timestamp();

-- name: PasswordChangedAlertReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('password_changed' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: PasswordChangedAlertKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='password_changed' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: CreatePasswordChangedAlertIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id)
VALUES($1,$2,$3,$4,$5,$6,'password_changed',1,$7,$8,$9,$10);


-- Snapshot proof state in one statement; expensive password verification and
-- hashing run without namespace/account locks. Locked revisions fence the result.
-- name: PasswordChangeProof :one
SELECT a.id,a.email,a.status,a.abuse_hold_at,a.mfa_enrolled_at,a.mfa_required,
    a.password_reset_revision,a.mfa_revision,coalesce(p.hash,'')::text AS hash,
    (p.account_id IS NOT NULL)::boolean AS has_password
FROM accounts a LEFT JOIN account_passwords p ON p.account_id=a.id WHERE a.id=$1;

-- name: LockPasswordChangeAccount :one
SELECT id,email,status,abuse_hold_at,password_reset_revision,mfa_revision FROM accounts WHERE id=$1 FOR UPDATE;

-- Legacy reset links share login_tokens. They have no saved address, namespace
-- or password revision; preserve that format while making use/password/alert atomic.
-- name: LockLegacyPasswordResetAccount :one
SELECT a.id,a.email,a.status,a.email_verified_at,a.password_reset_revision
FROM accounts a JOIN login_tokens t ON t.account_id=a.id WHERE t.token_hash=$1 FOR UPDATE OF a;

-- name: LockLegacyPasswordResetToken :one
SELECT token_hash,expires_at,consumed_at FROM login_tokens WHERE token_hash=$1 AND account_id=$2 FOR UPDATE;

-- name: ConsumeLegacyPasswordResetToken :execrows
UPDATE login_tokens SET consumed_at=clock_timestamp()
WHERE token_hash=$1 AND account_id=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp();

-- name: MFAEnabledAlertReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('mfa_enabled' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: MFAEnabledAlertKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='mfa_enabled' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: CreateMFAEnabledAlertIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id)
VALUES($1,$2,$3,$4,$5,$6,'mfa_enabled',1,$7,$8,$9,$10);



-- Confirmation verifies the secret only after acquiring the account lock;
-- no recovery-code hashes or password material are needed in this projection.
-- name: LockMFAEnabledEnrollmentAccount :one
SELECT id,email,status,abuse_hold_at,mfa_enrolled_at,mfa_secret_encrypted FROM accounts WHERE id=$1 FOR UPDATE;

-- Conditional preparation prevents an enrollment request that started earlier
-- from replacing a factor after another request has confirmed it.
-- name: SavePendingMFAEnrollment :execrows
UPDATE accounts SET mfa_secret_encrypted=$2,mfa_recovery_codes_hash=$3,mfa_revision=mfa_revision+1
WHERE id=$1 AND mfa_enrolled_at IS NULL;

-- name: ConfirmMFAEnabledEnrollment :one
UPDATE accounts SET mfa_enrolled_at=clock_timestamp(),mfa_required=false,mfa_revision=mfa_revision+1
WHERE id=$1 AND mfa_enrolled_at IS NULL AND mfa_secret_encrypted IS NOT NULL
RETURNING mfa_enrolled_at;

-- name: PaymentFailedReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('payment_failed' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: PaymentFailedKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='payment_failed' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: CreatePaymentFailedIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id,payment_past_due_at)
VALUES($1,$2,$3,$4,$5,$6,'payment_failed',1,$7,$8,$9,$10,$11);



-- name: PaymentFailedSchemaReady :exec
SELECT a.past_due_at,a.suspended_reason,o.payment_past_due_at,r.event_id,b.customer_id,g.status
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id
JOIN billing_payment_failure_receipts r ON r.account_id=a.id
JOIN billing_identities b ON b.account_id=a.id
LEFT JOIN orgs g ON g.personal_owner_account_id=a.id LIMIT 0;

-- name: LockPaymentFailedAccount :one
SELECT id,email,status FROM accounts WHERE id=$1 FOR UPDATE;

-- name: SharePaymentFailedIdentity :one
SELECT customer_id FROM billing_identities WHERE account_id=$1 AND provider=$2 AND customer_id=$3 FOR SHARE;

-- name: RecordPaymentFailureReceipt :execrows
INSERT INTO billing_payment_failure_receipts(provider,event_id,account_id,event_type)
VALUES($1,$2,$3,$4) ON CONFLICT(provider,event_id) DO NOTHING;

-- name: PaymentFailureReceipt :one
SELECT account_id,event_type FROM billing_payment_failure_receipts WHERE provider=$1 AND event_id=$2;

-- name: EnterPaymentPastDue :one
UPDATE accounts SET status='past_due',past_due_at=coalesce(past_due_at,clock_timestamp()),suspended_reason=NULL
WHERE id=$1 AND status='active' RETURNING past_due_at;

-- Maild reads only lifecycle metadata. It locks the account before its own
-- submission locks and holds that shared lock through durable submission start.
-- Missing intent/account/binding fails closed, including account erasure.
-- name: SharePaymentFailedSubmissionAccount :one
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,o.payment_past_due_at,o.from_address,m.sender_address
FROM internal_mail_messages m JOIN platform_mail_producer_outbox o
    ON o.namespace=m.namespace AND o.producer_id=m.producer_id
    AND m.idempotency_key='payment-failed:'||o.id::text AND o.template='payment_failed'
    AND o.expires_at=m.expires_at
JOIN accounts a ON a.id=o.account_id
WHERE m.namespace=$1 AND m.id=$2 FOR SHARE OF a;

-- name: PaymentFailedDeliveryReady :exec
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,o.payment_past_due_at
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id LIMIT 0;

-- Preserve UpdateAccountStatus's recovery behavior and personal-org mirror.
-- name: RestorePaymentAccount :execrows
UPDATE accounts SET status='active',past_due_at=NULL,deletion_requested_at=NULL,suspended_reason=NULL
WHERE id=$1 AND (status='past_due' OR (status IN ('suspended','deleted_pending') AND past_due_at IS NOT NULL));

-- name: RestorePaymentPersonalOrg :exec
UPDATE orgs SET status='active',updated_at=now() WHERE personal_org=true AND personal_owner_account_id=$1;

-- name: CancelPaymentFailedIntents :exec
UPDATE platform_mail_producer_outbox SET state='cancelled',code='account_changed',recipient_address='',
    lease_id=NULL,lease_until=NULL,sealed_security_alert='',security_alert_key_id='',
    updated_at=clock_timestamp(),revision=revision+1
WHERE account_id=$1 AND template='payment_failed' AND state IN ('queued','claimed');

-- name: AccountRestoredReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('account_restored' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: AccountRestoredKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='account_restored' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: CreateAccountRestoredIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id,account_restored_revision)
VALUES($1,$2,$3,$4,$5,$6,'account_restored',1,$7,$8,$9,$10,$11);



-- name: AccountRestoredSchemaReady :exec
SELECT a.billing_mail_revision,a.past_due_at,a.suspended_reason,o.account_restored_revision,r.event_id,b.customer_id,g.status
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id
JOIN billing_payment_recovery_receipts r ON r.account_id=a.id
JOIN billing_identities b ON b.account_id=a.id
LEFT JOIN orgs g ON g.personal_owner_account_id=a.id LIMIT 0;

-- name: AccountRestoredLifecycleReady :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_trigger g JOIN pg_catalog.pg_class t ON t.oid=g.tgrelid
    JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='accounts'
      AND g.tgname='accounts_billing_mail_revision_guard' AND g.tgenabled IN ('O','A'))::boolean AS ready;

-- name: LockAccountRestoredAccount :one
SELECT id,email,status,past_due_at FROM accounts WHERE id=$1 FOR UPDATE;

-- name: RecordPaymentRecoveryReceipt :execrows
INSERT INTO billing_payment_recovery_receipts(provider,event_id,account_id)
VALUES($1,$2,$3) ON CONFLICT(provider,event_id) DO NOTHING;

-- name: PaymentRecoveryReceipt :one
SELECT account_id FROM billing_payment_recovery_receipts WHERE provider=$1 AND event_id=$2;

-- The trigger advances the lifecycle revision for this exact restoration.
-- name: RestoreAccountWithConfirmation :one
UPDATE accounts SET status='active',past_due_at=NULL,deletion_requested_at=NULL,suspended_reason=NULL
WHERE id=$1 AND (status='past_due' OR (status IN ('suspended','deleted_pending') AND past_due_at IS NOT NULL))
RETURNING billing_mail_revision,clock_timestamp()::timestamptz AS restored_at;

-- name: ShareAccountRestoredSubmissionAccount :one
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,a.billing_mail_revision,
    o.account_restored_revision,o.from_address,m.sender_address
FROM internal_mail_messages m JOIN platform_mail_producer_outbox o
    ON o.namespace=m.namespace AND o.producer_id=m.producer_id
    AND m.idempotency_key='account-restored:'||o.id::text AND o.template='account_restored'
    AND o.expires_at=m.expires_at
JOIN accounts a ON a.id=o.account_id
WHERE m.namespace=$1 AND m.id=$2 FOR SHARE OF a;

-- name: AccountRestoredDeliveryReady :exec
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,a.billing_mail_revision,o.account_restored_revision
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id LIMIT 0;

-- name: AccountSuspendedReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('account_suspended' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: AccountSuspendedKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='account_suspended' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: AccountSuspendedSchemaReady :exec
SELECT a.billing_mail_revision,o.account_suspended_revision,o.suspension_past_due_at,p.followup_completed_at,p.lease_id
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id
JOIN billing_account_suspensions p ON p.intent_id=o.id LIMIT 0;

-- name: LockAccountSuspensionAccount :one
SELECT id,email,status,past_due_at,deletion_requested_at,billing_mail_revision,clock_timestamp()::timestamptz AS checked_at
FROM accounts WHERE id=$1 FOR UPDATE;

-- name: SuspendAccountWithNotice :one
UPDATE accounts SET status='suspended',suspended_reason=NULL
WHERE id=$1 AND status='past_due'
RETURNING billing_mail_revision,clock_timestamp()::timestamptz AS suspended_at;

-- name: CreateAccountSuspendedIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id,account_suspended_revision,suspension_past_due_at)
VALUES($1,$2,$3,$4,$5,$6,'account_suspended',1,$7,$8,$9,$10,$11,$12);

-- name: RecordAccountSuspension :exec
INSERT INTO billing_account_suspensions(account_id,past_due_at,intent_id,suspended_at,billing_revision)
VALUES($1,$2,$3,$4,$5);

-- name: AccountSuspensionPeriod :one
SELECT p.*,o.namespace,o.producer_id,o.from_address,o.recovery_id
FROM billing_account_suspensions p JOIN platform_mail_producer_outbox o ON o.id=p.intent_id
WHERE p.account_id=$1 AND p.past_due_at=$2 FOR UPDATE OF p;

-- name: ClaimSuspensionFollowup :one
UPDATE billing_account_suspensions SET lease_id=sqlc.arg(lease_id),lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
WHERE intent_id=sqlc.arg(intent_id) AND followup_completed_at IS NULL
    AND (lease_id IS NULL OR lease_until<=clock_timestamp() OR lease_id=sqlc.arg(lease_id))
RETURNING lease_id;

-- name: CompleteSuspensionFollowup :execrows
UPDATE billing_account_suspensions p SET followup_completed_at=coalesce(p.followup_completed_at,clock_timestamp())
FROM platform_mail_producer_outbox o
WHERE o.id=p.intent_id AND o.namespace=$1 AND o.producer_id=$2 AND p.account_id=$3 AND p.intent_id=$4
    AND p.lease_id=$5;

-- name: ShareAccountSuspendedSubmissionAccount :one
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,a.billing_mail_revision,
    o.account_suspended_revision,o.suspension_past_due_at,o.from_address,m.sender_address
FROM internal_mail_messages m JOIN platform_mail_producer_outbox o
    ON o.namespace=m.namespace AND o.producer_id=m.producer_id
    AND m.idempotency_key='account-suspended:'||o.id::text AND o.template='account_suspended'
    AND o.expires_at=m.expires_at
JOIN accounts a ON a.id=o.account_id
WHERE m.namespace=$1 AND m.id=$2 FOR SHARE OF a;

-- name: AccountSuspendedDeliveryReady :exec
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,a.billing_mail_revision,o.account_suspended_revision,o.suspension_past_due_at
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id LIMIT 0;

-- name: NonpaymentDeletionReady :one
-- Materialization keeps catalog deparsing behind the exact schema/relation
-- filter. Unrelated DDL can otherwise race pg_get_constraintdef's relation lookup.
WITH own_constraint AS MATERIALIZED (
    SELECT c.oid FROM pg_catalog.pg_constraint c
    JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
    WHERE n.nspname=current_schema() AND t.relname='platform_mail_producer_outbox'
      AND c.conname='platform_mail_producer_outbox_security_alert_check'
)
SELECT EXISTS(SELECT 1 FROM own_constraint c
    WHERE position('nonpayment_deletion' in pg_catalog.pg_get_constraintdef(c.oid))>0)::boolean AS ready;

-- name: NonpaymentDeletionKeyReferences :many
SELECT DISTINCT security_alert_key_id FROM platform_mail_producer_outbox
WHERE namespace=$1 AND producer_id=$2 AND template='nonpayment_deletion' AND state IN ('queued','claimed') AND expires_at>clock_timestamp();

-- name: NonpaymentDeletionSchemaReady :exec
SELECT a.billing_mail_revision,o.nonpayment_deletion_revision,o.nonpayment_past_due_at,o.nonpayment_deletion_at,p.followup_completed_at,p.lease_id
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id
JOIN billing_account_nonpayment_deletions p ON p.intent_id=o.id LIMIT 0;

-- name: LockNonpaymentDeletionAccount :one
SELECT id,email,status,past_due_at,deletion_requested_at,billing_mail_revision,clock_timestamp()::timestamptz AS checked_at
FROM accounts WHERE id=$1 FOR UPDATE;

-- name: ScheduleNonpaymentDeletionAccount :one
UPDATE accounts SET status='deleted_pending',suspended_reason=NULL,deletion_requested_at=clock_timestamp()
WHERE id=$1 AND status='suspended' AND deletion_requested_at IS NULL
RETURNING billing_mail_revision,deletion_requested_at;

-- name: CreateNonpaymentDeletionIntent :exec
INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
    template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id,nonpayment_deletion_revision,nonpayment_past_due_at,nonpayment_deletion_at)
VALUES($1,$2,$3,$4,$5,$6,'nonpayment_deletion',1,$7,$8,$9,$10,$11,$12,$13);

-- name: RecordNonpaymentDeletion :exec
INSERT INTO billing_account_nonpayment_deletions(account_id,past_due_at,intent_id,deletion_requested_at,billing_revision)
VALUES($1,$2,$3,$4,$5);

-- name: NonpaymentDeletionPeriod :one
SELECT p.*,o.namespace,o.producer_id,o.from_address,o.recovery_id
FROM billing_account_nonpayment_deletions p JOIN platform_mail_producer_outbox o ON o.id=p.intent_id
WHERE p.account_id=$1 AND p.past_due_at=$2 FOR UPDATE OF p;

-- name: ClaimNonpaymentDeletionFollowup :one
UPDATE billing_account_nonpayment_deletions SET lease_id=sqlc.arg(lease_id),lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
WHERE intent_id=sqlc.arg(intent_id) AND followup_completed_at IS NULL
    AND (lease_id IS NULL OR lease_until<=clock_timestamp() OR lease_id=sqlc.arg(lease_id))
RETURNING lease_id;

-- name: CompleteNonpaymentDeletionFollowup :execrows
UPDATE billing_account_nonpayment_deletions p SET followup_completed_at=coalesce(p.followup_completed_at,clock_timestamp())
FROM platform_mail_producer_outbox o
WHERE o.id=p.intent_id AND o.namespace=$1 AND o.producer_id=$2 AND p.account_id=$3 AND p.intent_id=$4
    AND p.lease_id=$5;

-- name: ShareNonpaymentDeletionSubmissionAccount :one
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,a.billing_mail_revision,
    o.nonpayment_deletion_revision,o.nonpayment_past_due_at,o.nonpayment_deletion_at,o.from_address,m.sender_address
FROM internal_mail_messages m JOIN platform_mail_producer_outbox o
    ON o.namespace=m.namespace AND o.producer_id=m.producer_id
    AND m.idempotency_key='nonpayment-deletion:'||o.id::text AND o.template='nonpayment_deletion'
    AND o.expires_at=m.expires_at
JOIN accounts a ON a.id=o.account_id
WHERE m.namespace=$1 AND m.id=$2 FOR SHARE OF a;

-- name: NonpaymentDeletionDeliveryReady :exec
SELECT a.email,a.status,a.past_due_at,a.deletion_requested_at,a.billing_mail_revision,o.nonpayment_deletion_revision,o.nonpayment_past_due_at,o.nonpayment_deletion_at
FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id LIMIT 0;

-- name: CancelPreDeletionDunningIntents :exec
UPDATE platform_mail_producer_outbox SET state='cancelled',code='account_changed',
    recipient_address='',lease_id=NULL,lease_until=NULL,sealed_security_alert='',security_alert_key_id='',
    updated_at=clock_timestamp(),revision=revision+1
WHERE account_id=$1 AND template IN ('payment_failed','account_suspended') AND state IN ('queued','claimed');
