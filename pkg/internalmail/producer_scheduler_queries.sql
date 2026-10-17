-- name: ProducerSchedulerSchema :exec
SELECT p.namespace,p.producer_id,p.last_turn,r.lease_id,r.started_at
FROM internal_mail_producer_dispatch p LEFT JOIN internal_mail_dispatch_reservations r
ON r.namespace=p.namespace AND r.producer_id=p.producer_id LIMIT 0;

-- Only fair claims take this namespace lock. Submission never takes it.
-- name: LockProducerScheduler :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text,871735));

-- name: AdvanceProducerTurn :exec
INSERT INTO internal_mail_producer_dispatch(namespace,producer_id,last_turn)
SELECT m.namespace,m.producer_id,
    coalesce((SELECT max(p.last_turn) FROM internal_mail_producer_dispatch p WHERE p.namespace=m.namespace),0)+1
FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
WHERE r.namespace=$1 AND r.id=$2
ON CONFLICT(namespace,producer_id) DO UPDATE SET last_turn=excluded.last_turn;

-- name: EnsureProducerDispatch :exec
INSERT INTO internal_mail_producer_dispatch(namespace,producer_id) VALUES($1,$2) ON CONFLICT DO NOTHING;

-- name: LockProducerDispatch :exec
SELECT last_turn FROM internal_mail_producer_dispatch WHERE namespace=$1 AND producer_id=$2 FOR UPDATE;

-- name: RecipientProducer :one
SELECT m.producer_id FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
WHERE r.namespace=$1 AND r.id=$2;

-- The producer row is locked before either query. Read the database clock after
-- that wait; no fixed minute boundaries or idle burst credits are involved.
-- name: PruneDispatchReservations :exec
DELETE FROM internal_mail_dispatch_reservations WHERE namespace=$1 AND producer_id=$2
AND started_at<=clock_timestamp()-sqlc.arg(window_seconds)::integer*interval '1 second';

-- name: ReserveProducerDispatch :execrows
WITH observed AS MATERIALIZED (SELECT clock_timestamp() AS at)
INSERT INTO internal_mail_dispatch_reservations(namespace,producer_id,lease_id,started_at)
SELECT sqlc.arg(namespace),sqlc.arg(producer_id),sqlc.arg(lease_id),observed.at FROM observed
WHERE (SELECT count(*) FROM internal_mail_dispatch_reservations r
    WHERE r.namespace=sqlc.arg(namespace) AND r.producer_id=sqlc.arg(producer_id)
      AND r.started_at>observed.at-sqlc.arg(window_seconds)::integer*interval '1 second')<sqlc.arg(dispatch_limit)::integer;

-- Configuration is trusted, bounded and contains the legacy bucket explicitly.
-- Private operator output has no addresses, payloads, tokens or identity keys.
-- name: ProducerDispatchStatus :many
WITH policy AS (
    SELECT unnest(sqlc.arg(producer_ids)::text[]) AS producer_id,
           unnest(sqlc.arg(dispatch_limits)::integer[]) AS dispatch_limit
), observed AS MATERIALIZED (SELECT clock_timestamp() AS at)
SELECT p.producer_id::text AS producer_id,p.dispatch_limit::integer AS dispatch_limit,
    (SELECT count(*) FROM internal_mail_dispatch_reservations r,observed o
     WHERE r.namespace=sqlc.arg(namespace) AND r.producer_id=p.producer_id
       AND r.started_at>o.at-sqlc.arg(window_seconds)::integer*interval '1 second')::bigint AS in_window,
    (SELECT min(releases.at) FROM (
        SELECT r.started_at+sqlc.arg(window_seconds)::integer*interval '1 second' AS at
        FROM internal_mail_dispatch_reservations r,observed o
        WHERE r.namespace=sqlc.arg(namespace) AND r.producer_id=p.producer_id
          AND r.started_at>o.at-sqlc.arg(window_seconds)::integer*interval '1 second'
        ORDER BY r.started_at OFFSET greatest(0,(SELECT count(*) FROM internal_mail_dispatch_reservations a,observed o
          WHERE a.namespace=sqlc.arg(namespace) AND a.producer_id=p.producer_id
          AND a.started_at>o.at-sqlc.arg(window_seconds)::integer*interval '1 second')-p.dispatch_limit)
        LIMIT 1
    ) releases)::timestamptz AS next_release_at,
    (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id,observed o
     WHERE r.namespace=sqlc.arg(namespace) AND m.producer_id=p.producer_id AND r.status IN ('queued','deferred')
       AND r.next_attempt_at<=o.at AND coalesce(m.send_at,m.created_at)<=o.at
       AND (m.expires_at IS NULL OR m.expires_at>o.at))::bigint AS due_recipients,
    coalesce((SELECT greatest(0,extract(epoch FROM o.at-min(coalesce(m.send_at,m.created_at))))::float8
     FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id,observed o
     WHERE r.namespace=sqlc.arg(namespace) AND m.producer_id=p.producer_id AND r.status IN ('queued','deferred')
       AND r.next_attempt_at<=o.at AND coalesce(m.send_at,m.created_at)<=o.at
       AND (m.expires_at IS NULL OR m.expires_at>o.at) GROUP BY o.at),0)::float8 AS oldest_due_seconds
FROM policy p ORDER BY p.producer_id;
