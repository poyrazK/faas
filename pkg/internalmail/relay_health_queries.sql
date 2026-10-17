-- name: RelayHealthSchema :exec
SELECT h.state,h.reason,e.revision FROM internal_mail_relay_health h CROSS JOIN internal_mail_relay_events e LIMIT 0;

-- name: EnsureRelayHealth :exec
INSERT INTO internal_mail_relay_health(namespace) VALUES($1) ON CONFLICT DO NOTHING;

-- The namespace submission advisory lock is held exclusively by every writer.
-- name: LockRelayHealth :one
SELECT * FROM internal_mail_relay_health WHERE namespace=$1 FOR UPDATE;

-- name: RelayDatabaseTime :one
SELECT clock_timestamp()::timestamptz AS current_time;

-- name: RelayHealth :one
SELECT * FROM internal_mail_relay_health WHERE namespace=$1;

-- name: SaveRelayHealth :exec
UPDATE internal_mail_relay_health SET state=$2,reason=$3,revision=$4,observation_id=$5,observed_at=$6,
    received_at=$7,inventory_ok=$8,smtp_up=$9,queue_messages=$10,oldest_seconds=$11,healthy_since=$12,updated_at=$13
WHERE namespace=$1;

-- name: InsertRelayEvent :exec
INSERT INTO internal_mail_relay_events(namespace,id,revision,state,reason,observation_id)
VALUES($1,$2,$3,$4,$5,$6);

-- name: RelayEvents :many
SELECT * FROM internal_mail_relay_events e WHERE e.namespace=sqlc.arg(namespace)
    AND (sqlc.arg(after_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR e.revision>(
        SELECT c.revision FROM internal_mail_relay_events c WHERE c.namespace=sqlc.arg(namespace) AND c.id=sqlc.arg(after_id)))
ORDER BY e.revision LIMIT sqlc.arg(page_limit);

-- The submission namespace lock excludes concurrent observations. Current time
-- after domain/recipient/pacing lock waits also fences a sample becoming stale.
-- name: RelayDispatchPermitted :one
SELECT EXISTS(SELECT 1 FROM internal_mail_relay_health WHERE namespace=sqlc.arg(namespace) AND state='healthy'
    AND observed_at>clock_timestamp()-sqlc.arg(max_sample_age_seconds)::integer*interval '1 second'
    AND received_at>clock_timestamp()-sqlc.arg(max_sample_age_seconds)::integer*interval '1 second'
    AND observed_at<=clock_timestamp()+sqlc.arg(clock_skew_seconds)::integer*interval '1 second'
    AND received_at<=clock_timestamp()+sqlc.arg(clock_skew_seconds)::integer*interval '1 second'
    AND inventory_ok AND smtp_up AND queue_messages<sqlc.arg(high_water)::integer
    AND (queue_messages=0 OR oldest_seconds+greatest(0,extract(epoch FROM clock_timestamp()-observed_at))<sqlc.arg(max_queue_age_seconds)::integer));
