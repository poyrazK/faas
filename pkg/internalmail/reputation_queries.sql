-- name: ReputationSchema :exec
SELECT f.recipient_id,h.held,e.sequence FROM internal_mail_reputation_feedback f
CROSS JOIN internal_mail_reputation_holds h CROSS JOIN internal_mail_reputation_events e LIMIT 0;

-- Caller holds the exclusive namespace submission lock before any recipient
-- locks. Only a newly observed kind changes the ledger; complaints take priority.
-- name: RecordReputationFeedback :execrows
INSERT INTO internal_mail_reputation_feedback(namespace,recipient_id,bounced_at,complained_at,bounce_event_id,complaint_event_id)
VALUES(sqlc.arg(namespace),sqlc.arg(recipient_id),
    CASE WHEN sqlc.arg(kind)::text='bounced' THEN clock_timestamp() END,
    CASE WHEN sqlc.arg(kind)::text='complained' THEN clock_timestamp() END,
    CASE WHEN sqlc.arg(kind)::text='bounced' THEN sqlc.arg(event_id)::text END,
    CASE WHEN sqlc.arg(kind)::text='complained' THEN sqlc.arg(event_id)::text END)
ON CONFLICT(namespace,recipient_id) DO UPDATE SET
    bounced_at=coalesce(internal_mail_reputation_feedback.bounced_at,excluded.bounced_at),
    bounce_event_id=coalesce(internal_mail_reputation_feedback.bounce_event_id,excluded.bounce_event_id),
    complained_at=coalesce(internal_mail_reputation_feedback.complained_at,excluded.complained_at),
    complaint_event_id=coalesce(internal_mail_reputation_feedback.complaint_event_id,excluded.complaint_event_id)
WHERE (sqlc.arg(kind)::text='complained' AND internal_mail_reputation_feedback.complained_at IS NULL)
   OR (sqlc.arg(kind)::text='bounced' AND internal_mail_reputation_feedback.bounced_at IS NULL
       AND internal_mail_reputation_feedback.complained_at IS NULL);

-- A release acknowledges all feedback received under the namespace fence up to
-- its updated_at. Aging rates may trip a hold later, but only with unreviewed
-- evidence. Operator review cannot be undone by replay or the next worker tick.
-- name: ReputationCandidates :many
WITH observed AS (
    SELECT f.recipient_id,coalesce(f.complaint_event_id,f.bounce_event_id) AS event_id,
        coalesce(f.complained_at,f.bounced_at) AS observed_at,m.domain_id,m.producer_id
    FROM internal_mail_reputation_feedback f
    JOIN internal_mail_recipients r ON r.namespace=f.namespace AND r.id=f.recipient_id
    JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
    WHERE f.namespace=sqlc.arg(namespace)
      AND coalesce(f.complained_at,f.bounced_at)>sqlc.arg(window_end)::timestamptz-sqlc.arg(window_seconds)::integer*interval '1 second'
      AND coalesce(f.complained_at,f.bounced_at)<=sqlc.arg(window_end)::timestamptz
), scopes AS (
    SELECT 'domain'::text AS scope,domain_id::text AS scope_key,recipient_id,event_id,observed_at FROM observed
    UNION ALL SELECT 'producer',producer_id,recipient_id,event_id,observed_at FROM observed
)
SELECT DISTINCT ON (s.scope,s.scope_key) s.scope,s.scope_key,s.recipient_id,s.event_id
FROM scopes s LEFT JOIN internal_mail_reputation_holds h
    ON h.namespace=sqlc.arg(namespace) AND h.scope=s.scope AND h.scope_key=s.scope_key
WHERE NOT coalesce(h.held,false)
  AND (h.updated_at IS NULL OR s.observed_at>h.updated_at)
  AND s.observed_at>sqlc.arg(window_end)::timestamptz-sqlc.arg(window_seconds)::integer*interval '1 second'
  AND s.observed_at<=sqlc.arg(window_end)::timestamptz
ORDER BY s.scope,s.scope_key,s.observed_at DESC,s.recipient_id;

-- The denominator is distinct recipients with their FIRST durable relay custody
-- evidence in the window, UNION recipients with first authenticated adverse
-- feedback in the window. The union keeps late feedback in both numerator and
-- denominator. Neither repeat reports nor retries add samples or move a clock.
-- name: ReputationWindow :one
WITH bounds AS (SELECT sqlc.arg(window_end)::timestamptz AS until,
    sqlc.arg(window_end)::timestamptz-sqlc.arg(window_seconds)::integer*interval '1 second' AS since),
members AS NOT MATERIALIZED (
    SELECT r.id FROM internal_mail_recipients r
    JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
    WHERE r.namespace=sqlc.arg(namespace)
      AND ((sqlc.arg(scope)::text='domain' AND m.domain_id::text=sqlc.arg(scope_key)::text)
        OR (sqlc.arg(scope)::text='producer' AND m.producer_id=sqlc.arg(scope_key)::text))
), exposures AS (
    SELECT DISTINCT e.recipient_id FROM internal_mail_events e
    JOIN members m ON m.id=e.recipient_id CROSS JOIN bounds b
    WHERE e.namespace=sqlc.arg(namespace) AND e.kind IN ('submitted','uncertain','accepted','bounced','complained')
      AND e.created_at>b.since AND e.created_at<=b.until
      AND NOT EXISTS (SELECT 1 FROM internal_mail_events earlier WHERE earlier.namespace=e.namespace
          AND earlier.recipient_id=e.recipient_id AND earlier.created_at<=b.since
          AND earlier.kind IN ('submitted','uncertain','accepted','bounced','complained'))
), bad AS (
    SELECT f.recipient_id,
        (f.complained_at IS NULL AND f.bounced_at>b.since AND f.bounced_at<=b.until) AS bounced,
        (f.complained_at>b.since AND f.complained_at<=b.until) AS complained
    FROM internal_mail_reputation_feedback f JOIN members m ON m.id=f.recipient_id CROSS JOIN bounds b
    WHERE f.namespace=sqlc.arg(namespace)
      AND coalesce(f.complained_at,f.bounced_at)>b.since AND coalesce(f.complained_at,f.bounced_at)<=b.until
), samples AS (
    SELECT e.recipient_id FROM exposures e
    UNION SELECT recipient_id FROM bad WHERE bounced OR complained
)
SELECT count(*)::bigint AS samples,
    count(*) FILTER (WHERE bad.bounced)::bigint AS bounces,
    count(*) FILTER (WHERE bad.complained)::bigint AS complaints
FROM samples s LEFT JOIN bad ON bad.recipient_id=s.recipient_id;

-- name: ReputationHold :one
SELECT * FROM internal_mail_reputation_holds WHERE namespace=$1 AND scope=$2 AND scope_key=$3;

-- name: SetReputationHold :one
INSERT INTO internal_mail_reputation_holds(namespace,scope,scope_key,held,revision,reason,samples,bounces,complaints,
    window_start,window_end,trigger_recipient_id,trigger_event_id)
VALUES($1,$2,$3,true,1,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(namespace,scope,scope_key) DO UPDATE SET held=true,revision=internal_mail_reputation_holds.revision+1,
    reason=excluded.reason,samples=excluded.samples,bounces=excluded.bounces,complaints=excluded.complaints,
    window_start=excluded.window_start,window_end=excluded.window_end,trigger_recipient_id=excluded.trigger_recipient_id,
    trigger_event_id=excluded.trigger_event_id,updated_at=clock_timestamp()
WHERE NOT internal_mail_reputation_holds.held RETURNING *;

-- name: ReleaseReputationHold :one
UPDATE internal_mail_reputation_holds SET held=false,revision=revision+1,updated_at=clock_timestamp()
WHERE namespace=$1 AND scope=$2 AND scope_key=$3 AND revision=$4 AND held RETURNING *;

-- name: ReputationHolds :many
SELECT * FROM internal_mail_reputation_holds WHERE namespace=sqlc.arg(namespace) AND held
    AND scope||':'||scope_key>sqlc.arg(after_key)::text
ORDER BY scope,scope_key LIMIT sqlc.arg(page_limit);

-- name: InsertReputationEvent :exec
INSERT INTO internal_mail_reputation_events(namespace,id,scope,scope_key,revision,held,reason,evidence,request_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: ReputationEvents :many
SELECT * FROM internal_mail_reputation_events e WHERE e.namespace=sqlc.arg(namespace)
    AND (sqlc.arg(after_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR e.sequence>(
        SELECT c.sequence FROM internal_mail_reputation_events c WHERE c.namespace=sqlc.arg(namespace) AND c.id=sqlc.arg(after_id)))
ORDER BY e.sequence LIMIT sqlc.arg(page_limit);

-- name: ReputationDispatchPermitted :one
SELECT NOT EXISTS (SELECT 1 FROM internal_mail_reputation_holds h
    JOIN internal_mail_messages m ON m.namespace=h.namespace
    JOIN internal_mail_recipients r ON r.namespace=m.namespace AND r.message_id=m.id
    WHERE r.namespace=$1 AND r.id=$2 AND h.held
      AND ((h.scope='domain' AND h.scope_key=m.domain_id::text) OR (h.scope='producer' AND h.scope_key=m.producer_id)));

-- name: ReputationStats :many
SELECT 'hold'::text AS category,scope AS label,reason AS detail,count(*)::bigint AS total
FROM internal_mail_reputation_holds h WHERE h.namespace=sqlc.arg(namespace) AND h.held GROUP BY h.scope,h.reason
UNION ALL
SELECT 'feedback','complained','',count(*) FROM internal_mail_reputation_feedback f
WHERE f.namespace=sqlc.arg(namespace) AND f.complained_at>statement_timestamp()-sqlc.arg(window_seconds)::integer*interval '1 second'
UNION ALL
SELECT 'feedback','bounced','',count(*) FROM internal_mail_reputation_feedback f
WHERE f.namespace=sqlc.arg(namespace) AND f.complained_at IS NULL AND f.bounced_at>statement_timestamp()-sqlc.arg(window_seconds)::integer*interval '1 second';
