-- name: PacingSchema :exec
SELECT namespace,recipient_domain,next_allowed_at,updated_at FROM internal_mail_domain_pacing LIMIT 0;

-- The caller already holds its recipient lock. INSERT handles concurrent first
-- use; the explicit subsequent row lock fences both first and existing slots.
-- name: EnsureDomainPacing :exec
INSERT INTO internal_mail_domain_pacing(namespace,recipient_domain,next_allowed_at)
VALUES($1,$2,clock_timestamp()) ON CONFLICT DO NOTHING;

-- name: LockDomainPacing :exec
SELECT next_allowed_at FROM internal_mail_domain_pacing WHERE namespace=$1 AND recipient_domain=$2 FOR UPDATE;

-- Applied after the row lock wait, using the actual database clock. A failed
-- BeginSubmission rolls this reservation back in the same transaction.
-- name: ReserveDomainPacing :execrows
UPDATE internal_mail_domain_pacing
SET next_allowed_at=clock_timestamp()+sqlc.arg(interval_seconds)::integer*interval '1 second',updated_at=clock_timestamp()
WHERE namespace=sqlc.arg(namespace) AND recipient_domain=sqlc.arg(recipient_domain) AND next_allowed_at<=clock_timestamp();

-- Pacing is queue eligibility, not a failed delivery attempt. No event is added.
-- name: SettlePacingWait :execrows
UPDATE internal_mail_recipients r SET status='queued',code='recipient_domain_paced',
    attempts=greatest(0,r.attempts-1),lease_id=NULL,lease_until=NULL,updated_at=clock_timestamp(),
    next_attempt_at=greatest(clock_timestamp(),coalesce((SELECT p.next_allowed_at FROM internal_mail_domain_pacing p
        WHERE p.namespace=r.namespace AND p.recipient_domain=lower(split_part(r.address,'@',2))),clock_timestamp()))
WHERE r.namespace=$1 AND r.id=$2 AND r.lease_id=$3 AND r.status='claimed' AND r.lease_until>clock_timestamp();

-- name: PacingStatistics :one
SELECT (SELECT count(*) FROM internal_mail_domain_pacing p WHERE p.namespace=$1 AND p.next_allowed_at>clock_timestamp())::bigint AS cooling_domains,
       (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_domain_pacing p
          ON p.namespace=r.namespace AND p.recipient_domain=lower(split_part(r.address,'@',2))
        WHERE r.namespace=$1 AND r.status IN ('queued','deferred') AND p.next_allowed_at>clock_timestamp()
          AND (r.next_attempt_at<=clock_timestamp() OR r.code='recipient_domain_paced'))::bigint AS waiting_recipients;
