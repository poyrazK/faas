-- name: EventFeedSchema :exec
SELECT s.last_position,e.position,c.checkpoint,r.to_position FROM internal_mail_event_streams s
CROSS JOIN internal_mail_event_feed e CROSS JOIN internal_mail_event_consumers c CROSS JOIN internal_mail_event_receipts r LIMIT 0;

-- name: LockEventPublication :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text,871733));

-- name: UnpublishedDeliveryEvents :many
SELECT e.id AS source_event_id,e.recipient_id,e.kind,e.created_at,m.id AS message_id,m.domain_id,m.producer_id
FROM internal_mail_events e JOIN internal_mail_recipients r ON r.namespace=e.namespace AND r.id=e.recipient_id
JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
WHERE e.namespace=$1 AND m.producer_id<>'' AND NOT EXISTS (
    SELECT 1 FROM internal_mail_event_feed f WHERE f.namespace=e.namespace AND f.source_event_id=e.id)
ORDER BY e.created_at,e.id LIMIT $2;

-- name: NextEventPosition :one
INSERT INTO internal_mail_event_streams(namespace,producer_id,last_position) VALUES($1,$2,1)
ON CONFLICT(namespace,producer_id) DO UPDATE SET last_position=internal_mail_event_streams.last_position+1
RETURNING last_position;

-- name: PublishDeliveryEvent :exec
INSERT INTO internal_mail_event_feed(namespace,producer_id,position,id,source_event_id,message_id,recipient_id,domain_id,kind,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- Serialize creation budgets, pulls, acknowledgments and operator rewinds for
-- one producer. This lock never touches SMTP submission or publication locks.
-- name: LockEventConsumers :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text||':'||sqlc.arg(producer_id)::text,871734));

-- name: EventConsumerCount :one
SELECT count(*)::bigint FROM internal_mail_event_consumers WHERE namespace=$1 AND producer_id=$2;

-- name: CreateEventConsumer :one
INSERT INTO internal_mail_event_consumers(namespace,producer_id,name) VALUES($1,$2,$3) RETURNING *;

-- name: EventConsumer :one
SELECT * FROM internal_mail_event_consumers WHERE namespace=$1 AND producer_id=$2 AND name=$3;

-- name: DeliveryEventBatch :many
SELECT * FROM internal_mail_event_feed WHERE namespace=sqlc.arg(namespace) AND producer_id=sqlc.arg(producer_id)
    AND position>sqlc.arg(after_position)::bigint
    AND (sqlc.arg(through_position)::bigint=0 OR position<=sqlc.arg(through_position)::bigint)
ORDER BY position LIMIT sqlc.arg(page_limit);

-- name: DeliveryEventAt :one
SELECT * FROM internal_mail_event_feed WHERE namespace=$1 AND producer_id=$2 AND position=$3;

-- name: DeliveryEventCursor :one
SELECT position FROM internal_mail_event_feed WHERE namespace=$1 AND producer_id=$2 AND id=$3;

-- name: CreateEventReceipt :one
INSERT INTO internal_mail_event_receipts(namespace,producer_id,consumer,id,generation,from_position,to_position)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: EventReceipt :one
SELECT * FROM internal_mail_event_receipts WHERE namespace=$1 AND producer_id=$2 AND consumer=$3 AND id=$4;

-- name: SetPendingEventReceipt :exec
UPDATE internal_mail_event_consumers SET pending_receipt=$4,last_pull_at=clock_timestamp()
WHERE namespace=$1 AND producer_id=$2 AND name=$3;

-- name: AcknowledgeEventReceipt :exec
UPDATE internal_mail_event_receipts SET acknowledged_at=clock_timestamp()
WHERE namespace=$1 AND producer_id=$2 AND consumer=$3 AND id=$4;

-- name: AdvanceEventConsumer :exec
UPDATE internal_mail_event_consumers SET checkpoint=$4,pending_receipt=NULL,last_ack_at=clock_timestamp()
WHERE namespace=$1 AND producer_id=$2 AND name=$3;

-- name: ReplayEventConsumer :exec
UPDATE internal_mail_event_consumers SET checkpoint=$4,generation=generation+1,pending_receipt=NULL,last_ack_at=NULL
WHERE namespace=$1 AND producer_id=$2 AND name=$3;

-- name: EventConsumerStatus :one
SELECT c.*,coalesce(f.id,'00000000-0000-0000-0000-000000000000'::uuid) AS cursor,
    (SELECT count(*) FROM internal_mail_event_feed e WHERE e.namespace=c.namespace AND e.producer_id=c.producer_id AND e.position>c.checkpoint)::bigint AS backlog,
    coalesce((SELECT greatest(0,extract(epoch FROM clock_timestamp()-min(e.published_at)))::float8
        FROM internal_mail_event_feed e WHERE e.namespace=c.namespace AND e.producer_id=c.producer_id AND e.position>c.checkpoint),0)::float8 AS oldest_seconds
FROM internal_mail_event_consumers c LEFT JOIN internal_mail_event_feed f
    ON f.namespace=c.namespace AND f.producer_id=c.producer_id AND f.position=c.checkpoint
WHERE c.namespace=$1 AND c.producer_id=$2 AND c.name=$3;

-- name: EventConsumerNames :many
SELECT name FROM internal_mail_event_consumers WHERE namespace=$1 AND producer_id=$2 AND name>$3 ORDER BY name LIMIT $4;

-- name: EventFeedStats :one
WITH consumers AS (
    SELECT c.pending_receipt,
        (SELECT count(*) FROM internal_mail_event_feed e WHERE e.namespace=c.namespace AND e.producer_id=c.producer_id AND e.position>c.checkpoint)::bigint AS backlog,
        (SELECT min(e.published_at) FROM internal_mail_event_feed e WHERE e.namespace=c.namespace AND e.producer_id=c.producer_id AND e.position>c.checkpoint) AS oldest
    FROM internal_mail_event_consumers c WHERE c.namespace=$1
)
SELECT (SELECT count(*) FROM internal_mail_events e
    JOIN internal_mail_recipients r ON r.namespace=e.namespace AND r.id=e.recipient_id
    JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
    WHERE e.namespace=$1 AND m.producer_id<>'' AND NOT EXISTS (
        SELECT 1 FROM internal_mail_event_feed f WHERE f.namespace=e.namespace AND f.source_event_id=e.id))::bigint AS unpublished,
    (SELECT count(*) FROM internal_mail_event_feed WHERE namespace=$1)::bigint AS published,
    count(*)::bigint AS consumers,count(*) FILTER (WHERE pending_receipt IS NOT NULL)::bigint AS pending_batches,
    coalesce(sum(backlog),0)::bigint AS unacked,coalesce(max(backlog),0)::bigint AS max_backlog,
    coalesce(greatest(0,extract(epoch FROM clock_timestamp()-min(oldest)))::float8,0)::float8 AS oldest_seconds
FROM consumers;
