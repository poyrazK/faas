-- name: CreateDomain :one
INSERT INTO internal_mail_domains (id,namespace,name,selector,verification_token,public_key,sealed_key)
VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (namespace,name) DO NOTHING RETURNING *;

-- name: Domain :one
SELECT * FROM internal_mail_domains WHERE namespace=$1 AND id=$2;

-- name: DomainByName :one
SELECT * FROM internal_mail_domains WHERE namespace=$1 AND name=$2;

-- name: SetDomainStatus :execrows
UPDATE internal_mail_domains SET status=$3 WHERE namespace=$1 AND id=$2;

-- name: LockNamespace :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text, 871726));

-- name: MessageByKey :one
SELECT * FROM internal_mail_messages WHERE namespace=$1 AND producer_id=$2 AND idempotency_key=$3;

-- name: Message :one
SELECT * FROM internal_mail_messages WHERE namespace=$1 AND id=$2;

-- name: Quota :one
SELECT count(*) FILTER (WHERE m.created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::bigint AS daily,
       count(*) FILTER (WHERE r.status IN ('queued','claimed','submitting','deferred','uncertain'))::bigint AS pending
FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id AND m.namespace=r.namespace
WHERE r.namespace=$1;

-- name: CreateMessage :one
INSERT INTO internal_mail_messages(id,namespace,domain_id,idempotency_key,sealed_payload,payload_fingerprint,producer_id,sender_address,send_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *;

-- name: CreateRecipient :exec
INSERT INTO internal_mail_recipients(id,namespace,message_id,address,next_attempt_at,recovery_id,suppression_revision)
VALUES($1,$2,$3,$4,coalesce(sqlc.narg(next_attempt_at)::timestamptz,now()),
    coalesce((SELECT s.recovery_id FROM internal_mail_recovery_state s WHERE s.namespace=$2),'00000000-0000-0000-0000-000000000000'::uuid),
    CASE WHEN EXISTS(SELECT 1 FROM internal_mail_suppressions active WHERE active.namespace=$2 AND active.address=lower($4::text)) THEN 0
    ELSE coalesce((SELECT state.last_block_revision FROM internal_mail_suppression_state state WHERE state.namespace=$2 AND state.address=lower($4::text)),0) END);

-- name: Recipients :many
SELECT * FROM internal_mail_recipients WHERE namespace=$1 AND message_id=$2 ORDER BY id;

-- name: Events :many
SELECT e.* FROM internal_mail_events e JOIN internal_mail_recipients r ON r.id=e.recipient_id AND r.namespace=e.namespace
WHERE r.namespace=$1 AND r.message_id=$2 ORDER BY e.created_at,e.id LIMIT $3;

-- name: Claim :one
WITH candidate AS (
    SELECT ready.id FROM internal_mail_recipients ready
    JOIN internal_mail_messages m ON m.namespace=ready.namespace AND m.id=ready.message_id
    JOIN internal_mail_domains d ON d.namespace=m.namespace AND d.id=m.domain_id
    LEFT JOIN internal_mail_producer_dispatch dispatch ON dispatch.namespace=m.namespace AND dispatch.producer_id=m.producer_id
    WHERE ready.namespace=sqlc.arg(namespace) AND ready.attempts < sqlc.arg(max_attempts)
      AND NOT EXISTS (SELECT 1 FROM internal_mail_recovery_state recovery WHERE recovery.namespace=ready.namespace AND (recovery.held OR recovery.recovery_id<>ready.recovery_id))
      AND (NOT sqlc.arg(relay_enabled)::boolean OR EXISTS (SELECT 1 FROM internal_mail_relay_health h
          WHERE h.namespace=ready.namespace AND h.state='healthy'
            AND h.observed_at>clock_timestamp()-sqlc.arg(relay_max_age_seconds)::integer*interval '1 second'
            AND h.received_at>clock_timestamp()-sqlc.arg(relay_max_age_seconds)::integer*interval '1 second'
            AND h.observed_at<=clock_timestamp()+sqlc.arg(relay_clock_skew_seconds)::integer*interval '1 second'
            AND h.received_at<=clock_timestamp()+sqlc.arg(relay_clock_skew_seconds)::integer*interval '1 second'
            AND h.inventory_ok AND h.smtp_up AND h.queue_messages<sqlc.arg(relay_high_water)::integer
            AND (h.queue_messages=0 OR h.oldest_seconds+greatest(0,extract(epoch FROM clock_timestamp()-h.observed_at))<sqlc.arg(relay_max_queue_age_seconds)::integer)))
      AND ((m.send_at IS NULL AND m.expires_at IS NULL) OR sqlc.arg(scheduling_enabled)::boolean)
      AND (m.send_at IS NULL OR m.send_at<=clock_timestamp())
      AND (m.expires_at IS NULL OR m.expires_at>clock_timestamp())
      AND (NOT sqlc.arg(pacing_enabled)::boolean OR NOT EXISTS (
          SELECT 1 FROM internal_mail_domain_pacing p WHERE p.namespace=ready.namespace
            AND p.recipient_domain=lower(split_part(ready.address,'@',2)) AND p.next_allowed_at>clock_timestamp()))
      AND ((m.send_at IS NULL AND m.expires_at IS NULL) OR coalesce(m.send_at,m.created_at)>clock_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second')
      AND (m.producer_id='' OR (m.producer_id||':'||m.sender_address)=ANY(sqlc.arg(allowed_producer_senders)::text[]))
      AND NOT EXISTS (SELECT 1 FROM internal_mail_producer_access a WHERE a.namespace=m.namespace AND a.producer_id=m.producer_id AND a.suspended)
      AND (NOT sqlc.arg(producer_scheduling_enabled)::boolean OR EXISTS (
          SELECT 1 FROM (SELECT unnest(sqlc.arg(dispatch_producer_ids)::text[]) AS producer_id,
              unnest(sqlc.arg(dispatch_limits)::integer[]) AS dispatch_limit) policy
          WHERE policy.producer_id=m.producer_id AND (SELECT count(*) FROM internal_mail_dispatch_reservations reservation
              WHERE reservation.namespace=m.namespace AND reservation.producer_id=m.producer_id
                AND reservation.started_at>clock_timestamp()-sqlc.arg(dispatch_window_seconds)::integer*interval '1 second')<policy.dispatch_limit))
      AND (NOT sqlc.arg(reputation_enabled)::boolean OR NOT EXISTS (
          SELECT 1 FROM internal_mail_reputation_holds h WHERE h.namespace=ready.namespace AND h.held
            AND ((h.scope='domain' AND h.scope_key=m.domain_id::text) OR (h.scope='producer' AND h.scope_key=m.producer_id))))
      AND d.authorization_state<>'paused'
      AND NOT EXISTS (SELECT 1 FROM internal_mail_delivery_controls c WHERE c.namespace=ready.namespace
          AND c.scope_id IN ('00000000-0000-0000-0000-000000000000'::uuid,d.id) AND c.mode<>'running')
      AND (NOT sqlc.arg(require_fresh)::boolean OR (d.last_verified_at>=now()-sqlc.arg(max_check_age_seconds)::integer*interval '1 second' AND d.verified_sending_ip=sqlc.arg(sending_ip)))
      AND ((ready.status IN ('queued','deferred') AND (ready.next_attempt_at<=clock_timestamp()
          OR (NOT sqlc.arg(pacing_enabled)::boolean AND ready.status='queued' AND ready.code='recipient_domain_paced')))
        OR (ready.status='claimed' AND ready.lease_until<clock_timestamp()))
    ORDER BY CASE WHEN sqlc.arg(producer_scheduling_enabled)::boolean THEN coalesce(dispatch.last_turn,0) ELSE 0 END,
      CASE WHEN sqlc.arg(producer_scheduling_enabled)::boolean THEN m.producer_id ELSE '' END,
      ready.next_attempt_at,ready.id FOR UPDATE OF ready SKIP LOCKED LIMIT 1
)
UPDATE internal_mail_recipients r SET status='claimed', attempts=r.attempts+1,
    lease_id=sqlc.arg(lease_id), lease_until=clock_timestamp()+sqlc.arg(lease_seconds)::integer * interval '1 second', updated_at=clock_timestamp()
FROM candidate c WHERE r.id=c.id RETURNING r.*;

-- name: RecoverSubmissions :exec
WITH expired AS (
    UPDATE internal_mail_recipients r SET status='uncertain',code='submission_lease_expired',lease_id=NULL,lease_until=NULL,updated_at=now()
    WHERE r.namespace=$1 AND r.status='submitting' AND r.lease_until<now() RETURNING r.*
)
INSERT INTO internal_mail_events(id,namespace,recipient_id,kind,code)
SELECT 'uncertain:'||id::text||':'||attempts::text,namespace,id,'uncertain',code FROM expired ON CONFLICT DO NOTHING;

-- name: RecoverReportedSubmission :exec
WITH expired AS (
    UPDATE internal_mail_recipients r SET status='uncertain',code='submission_lease_expired',lease_id=NULL,lease_until=NULL,updated_at=now()
    WHERE r.namespace=$1 AND r.id=$2 AND r.status='submitting' AND r.lease_until<now() RETURNING r.*
)
INSERT INTO internal_mail_events(id,namespace,recipient_id,kind,code)
SELECT 'uncertain:'||id::text||':'||attempts::text,namespace,id,'uncertain',code FROM expired ON CONFLICT DO NOTHING;

-- name: ExpireRecipients :exec
WITH expired AS (
    UPDATE internal_mail_recipients r SET
      status=CASE WHEN (m.send_at IS NOT NULL OR m.expires_at IS NOT NULL)
          AND (m.expires_at<=statement_timestamp() OR coalesce(m.send_at,m.created_at)<=statement_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second')
        THEN 'expired' ELSE 'failed' END,
      code=CASE WHEN (m.send_at IS NOT NULL OR m.expires_at IS NOT NULL)
          AND (m.expires_at<=statement_timestamp() OR coalesce(m.send_at,m.created_at)<=statement_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second')
        THEN 'message_expired' ELSE 'attempt_or_age_limit' END,
      lease_id=NULL,lease_until=NULL,updated_at=statement_timestamp()
    FROM internal_mail_messages m
    WHERE r.namespace=sqlc.arg(namespace) AND r.message_id=m.id AND m.namespace=r.namespace
      AND NOT EXISTS (SELECT 1 FROM internal_mail_recovery_state recovery WHERE recovery.namespace=r.namespace AND (recovery.held OR recovery.recovery_id<>r.recovery_id))
      AND (NOT sqlc.arg(timed_only)::boolean OR m.send_at IS NOT NULL OR m.expires_at IS NOT NULL)
      AND ((r.status IN ('queued','deferred','claimed') AND (m.send_at IS NOT NULL OR m.expires_at IS NOT NULL)
          AND (m.expires_at<=statement_timestamp() OR coalesce(m.send_at,m.created_at)<=statement_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second'))
        OR ((r.status IN ('queued','deferred') OR (r.status='claimed' AND r.lease_until<statement_timestamp()))
          AND (r.attempts>=sqlc.arg(max_attempts) OR coalesce(m.send_at,m.created_at)<statement_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second')))
    RETURNING r.*
)
INSERT INTO internal_mail_events(id,namespace,recipient_id,kind,code)
SELECT 'expired:'||id::text||':'||attempts::text,namespace,id,status,code FROM expired ON CONFLICT DO NOTHING;

-- name: BeginSubmission :execrows
UPDATE internal_mail_recipients r SET status='submitting',updated_at=now(),signing_selector=d.selector
FROM internal_mail_messages m JOIN internal_mail_domains d ON d.id=m.domain_id AND d.namespace=m.namespace
WHERE r.namespace=$1 AND r.id=$2 AND r.lease_id=$3 AND r.status='claimed' AND r.lease_until>clock_timestamp()
  AND (NOT sqlc.arg(relay_enabled)::boolean OR EXISTS (SELECT 1 FROM internal_mail_relay_health h
      WHERE h.namespace=r.namespace AND h.state='healthy'
        AND h.observed_at>clock_timestamp()-sqlc.arg(relay_max_age_seconds)::integer*interval '1 second'
        AND h.received_at>clock_timestamp()-sqlc.arg(relay_max_age_seconds)::integer*interval '1 second'
        AND h.observed_at<=clock_timestamp()+sqlc.arg(relay_clock_skew_seconds)::integer*interval '1 second'
        AND h.received_at<=clock_timestamp()+sqlc.arg(relay_clock_skew_seconds)::integer*interval '1 second'
        AND h.inventory_ok AND h.smtp_up AND h.queue_messages<sqlc.arg(relay_high_water)::integer
        AND (h.queue_messages=0 OR h.oldest_seconds+greatest(0,extract(epoch FROM clock_timestamp()-h.observed_at))<sqlc.arg(relay_max_queue_age_seconds)::integer)))
  AND m.id=r.message_id AND m.namespace=r.namespace AND d.status='verified'
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recovery_state recovery WHERE recovery.namespace=r.namespace AND (recovery.held OR recovery.recovery_id<>r.recovery_id))
  AND (NOT sqlc.arg(reputation_enabled)::boolean OR NOT EXISTS (
      SELECT 1 FROM internal_mail_reputation_holds h WHERE h.namespace=r.namespace AND h.held
        AND ((h.scope='domain' AND h.scope_key=m.domain_id::text) OR (h.scope='producer' AND h.scope_key=m.producer_id))))
  AND ((m.send_at IS NULL AND m.expires_at IS NULL) OR sqlc.arg(scheduling_enabled)::boolean)
  AND (m.send_at IS NULL OR m.send_at<=clock_timestamp())
  AND (m.expires_at IS NULL OR m.expires_at>clock_timestamp())
  AND ((m.send_at IS NULL AND m.expires_at IS NULL) OR coalesce(m.send_at,m.created_at)>clock_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second')
  AND (m.producer_id='' OR (m.producer_id||':'||m.sender_address)=ANY(sqlc.arg(allowed_producer_senders)::text[]))
  AND NOT EXISTS (SELECT 1 FROM internal_mail_producer_access a WHERE a.namespace=m.namespace AND a.producer_id=m.producer_id AND a.suspended)
  AND NOT EXISTS (SELECT 1 FROM internal_mail_suppression_state s WHERE s.namespace=r.namespace AND s.address=lower(r.address) AND s.last_block_revision>r.suppression_revision)
  AND d.authorization_state<>'paused' AND (sqlc.arg(expected_selector)::text='' OR d.selector=sqlc.arg(expected_selector))
  AND NOT EXISTS (SELECT 1 FROM internal_mail_suppressions s WHERE s.namespace=r.namespace AND s.address=lower(r.address));

-- name: Settle :execrows
UPDATE internal_mail_recipients SET status=$4,code=$5,next_attempt_at=$6,lease_id=NULL,lease_until=NULL,updated_at=now()
    ,attempts=CASE WHEN status='claimed' AND $4='queued' AND $5 IN ('recovery_held','delivery_paused','producer_unavailable','scheduling_disabled','message_not_due','relay_backpressure','reputation_held','producer_throttled','producer_suspended','encryption_key_unavailable') THEN greatest(0,attempts-1) ELSE attempts END
WHERE namespace=$1 AND id=$2 AND lease_id=$3 AND status IN ('claimed','submitting') AND lease_until>now();

-- name: Suppressed :one
SELECT EXISTS(SELECT 1 FROM internal_mail_suppressions WHERE namespace=$1 AND address=lower($2::text));

-- name: InsertEvent :exec
INSERT INTO internal_mail_events(id,namespace,recipient_id,kind,code) VALUES($1,$2,$3,$4,$5);

-- name: Event :one
SELECT * FROM internal_mail_events WHERE namespace=$1 AND id=$2;

-- name: LockRecipient :one
SELECT * FROM internal_mail_recipients WHERE namespace=$1 AND id=$2 FOR UPDATE;

-- name: ReportMessageDomain :one
SELECT d.name FROM internal_mail_messages m
JOIN internal_mail_domains d ON d.id=m.domain_id AND d.namespace=m.namespace
WHERE m.namespace=$1 AND m.id=$2;

-- name: ReportStatus :exec
UPDATE internal_mail_recipients SET status=$3,code=$4,next_attempt_at=now(),updated_at=now() WHERE namespace=$1 AND id=$2;

-- name: Suppress :exec
INSERT INTO internal_mail_suppressions(namespace,address,reason) VALUES($1,lower($2::text),$3)
ON CONFLICT(namespace,address) DO UPDATE SET reason=CASE WHEN internal_mail_suppressions.reason='complained' THEN 'complained' ELSE excluded.reason END;

-- name: MonitoringStats :many
SELECT 'recipient'::text AS category, r.status AS label, count(*)::bigint AS total,
       greatest(0,extract(epoch FROM now()-min(coalesce(m.send_at,m.created_at))))::float8 AS oldest_seconds,
       greatest(0,extract(epoch FROM now()-min(greatest(r.updated_at,coalesce(m.send_at,r.updated_at)))))::float8 AS state_age_seconds
FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id AND m.namespace=r.namespace
WHERE r.namespace=$1 GROUP BY r.status
UNION ALL
SELECT 'event',kind,count(*)::bigint,0::float8,0::float8 FROM internal_mail_events
WHERE namespace=$1 AND created_at>=now()-interval '24 hours' GROUP BY kind
UNION ALL
SELECT 'outcome',status,count(*)::bigint,0::float8,0::float8 FROM internal_mail_recipients
WHERE namespace=$1 AND updated_at>=now()-interval '24 hours' AND status IN ('accepted','bounced','failed','complained') GROUP BY status
UNION ALL
SELECT 'suppression','all',count(*)::bigint,0::float8,0::float8 FROM internal_mail_suppressions WHERE namespace=$1;
