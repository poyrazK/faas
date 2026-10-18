-- name: SchedulingSchema :exec
SELECT send_at,expires_at FROM internal_mail_messages LIMIT 0;

-- Applied only after the existing scoped idempotency lookup. An identical
-- replay remains valid after its original admission window has passed.
-- name: TimingAdmission :one
SELECT (sqlc.narg(send_at)::timestamptz IS NULL
        OR (sqlc.narg(send_at)>clock_timestamp() AND sqlc.narg(send_at)<=now()+sqlc.arg(max_delay_seconds)::integer*interval '1 second'))
   AND (sqlc.narg(expires_at)::timestamptz IS NULL
        OR (sqlc.narg(expires_at)>greatest(clock_timestamp(),coalesce(sqlc.narg(send_at),now()))
            AND sqlc.narg(expires_at)<=coalesce(sqlc.narg(send_at),now())+sqlc.arg(max_age_seconds)::integer*interval '1 second')) AS allowed;

-- The caller holds the domain and recipient row locks before this check.
-- clock_timestamp deliberately observes time after any lock wait.
-- name: RecipientTiming :one
SELECT CASE
    WHEN (m.send_at IS NOT NULL OR m.expires_at IS NOT NULL)
      AND (m.expires_at<=clock_timestamp() OR coalesce(m.send_at,m.created_at)<=clock_timestamp()-sqlc.arg(max_age_seconds)::integer*interval '1 second') THEN 'expired'
    WHEN (m.send_at IS NOT NULL OR m.expires_at IS NOT NULL) AND NOT sqlc.arg(scheduling_enabled)::boolean THEN 'disabled'
    WHEN m.send_at>clock_timestamp() THEN 'waiting'
    ELSE 'ready' END::text AS disposition
FROM internal_mail_messages m JOIN internal_mail_recipients r ON r.namespace=m.namespace AND r.message_id=m.id
WHERE r.namespace=sqlc.arg(namespace) AND r.id=sqlc.arg(recipient_id);

-- name: SchedulingStatistics :one
SELECT count(*) FILTER (WHERE m.send_at>clock_timestamp() AND r.status IN ('queued','deferred'))::bigint AS scheduled_waiting,
       count(*) FILTER (WHERE m.expires_at IS NOT NULL AND r.status IN ('queued','deferred','claimed'))::bigint AS expiring_pending
FROM internal_mail_messages m JOIN internal_mail_recipients r ON r.namespace=m.namespace AND r.message_id=m.id
WHERE m.namespace=$1;
