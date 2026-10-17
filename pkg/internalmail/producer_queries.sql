-- name: ProducersSchema :exec
SELECT producer_id,sender_address FROM internal_mail_messages LIMIT 0;

-- name: ProducerQuota :one
SELECT count(*) FILTER (WHERE m.created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::bigint AS daily,
       count(*) FILTER (WHERE r.status IN ('queued','claimed','submitting','deferred','uncertain'))::bigint AS pending
FROM internal_mail_messages m JOIN internal_mail_recipients r ON r.namespace=m.namespace AND r.message_id=m.id
WHERE m.namespace=$1 AND m.producer_id=$2;

-- Producer projections never fetch payloads, fingerprints, idempotency keys or
-- credentials. Ownership is immutable and checked in SQL before other reads.
-- name: ProducerMessage :one
SELECT id,domain_id,created_at,payload_pruned_at,producer_id,sender_address,send_at,expires_at
FROM internal_mail_messages WHERE namespace=$1 AND producer_id=$2 AND id=$3;
