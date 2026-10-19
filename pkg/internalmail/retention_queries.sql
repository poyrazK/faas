-- All eligibility checks deliberately name allowed terminal states. Unknown,
-- in-flight, relay-pending and uncertain recipients keep the payload. A fresh
-- terminal observation restarts the quiet period. Preview and apply use the
-- same predicate; apply checks it again after locking every recipient.

-- name: RetentionSchema :exec
SELECT payload_fingerprint,payload_pruned_at FROM internal_mail_messages LIMIT 0;

-- name: PreviewRetention :many
SELECT octet_length(m.sealed_payload)::bigint AS payload_bytes
FROM internal_mail_messages m
WHERE m.namespace=sqlc.arg(namespace)
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recovery_state recovery WHERE recovery.namespace=m.namespace AND recovery.held)
  AND m.payload_pruned_at IS NULL
  AND m.created_at<now()-sqlc.arg(retention_days)::integer * interval '1 day'
  AND EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id
    AND (r.status NOT IN ('accepted','bounced','complained','suppressed','failed','cancelled','expired')
         OR r.updated_at>=now()-sqlc.arg(retention_days)::integer * interval '1 day'))
ORDER BY m.created_at,m.id LIMIT sqlc.arg(batch_size)::integer;

-- name: RetentionCandidates :many
SELECT m.id,octet_length(m.sealed_payload)::bigint AS payload_bytes,
       (SELECT count(*) FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)::bigint AS recipient_count
FROM internal_mail_messages m
WHERE m.namespace=sqlc.arg(namespace)
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recovery_state recovery WHERE recovery.namespace=m.namespace AND recovery.held)
  AND m.payload_pruned_at IS NULL
  AND m.created_at<now()-sqlc.arg(retention_days)::integer * interval '1 day'
  AND EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id
    AND (r.status NOT IN ('accepted','bounced','complained','suppressed','failed','cancelled','expired')
         OR r.updated_at>=now()-sqlc.arg(retention_days)::integer * interval '1 day'))
ORDER BY m.created_at,m.id LIMIT sqlc.arg(batch_size)::integer FOR UPDATE OF m SKIP LOCKED;

-- name: LockRetentionRecipients :many
SELECT id FROM internal_mail_recipients WHERE namespace=$1 AND message_id=$2
ORDER BY id FOR UPDATE SKIP LOCKED;

-- name: PrunePayload :execrows
UPDATE internal_mail_messages m SET sealed_payload=''::bytea,payload_fingerprint=sqlc.arg(fingerprint),payload_pruned_at=now()
WHERE m.namespace=sqlc.arg(namespace) AND m.id=sqlc.arg(id) AND m.payload_pruned_at IS NULL
  AND m.created_at<now()-sqlc.arg(retention_days)::integer * interval '1 day'
  AND EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id
    AND (r.status NOT IN ('accepted','bounced','complained','suppressed','failed','cancelled','expired')
         OR r.updated_at>=now()-sqlc.arg(retention_days)::integer * interval '1 day'));

-- name: RetentionStatistics :one
WITH inventory AS (
  SELECT m.payload_pruned_at,octet_length(m.sealed_payload)::bigint AS payload_bytes,m.created_at,
    m.payload_pruned_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM internal_mail_recovery_state recovery WHERE recovery.namespace=m.namespace AND recovery.held)
    AND m.created_at<now()-sqlc.arg(retention_days)::integer * interval '1 day'
    AND EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)
    AND NOT EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id
      AND (r.status NOT IN ('accepted','bounced','complained','suppressed','failed','cancelled','expired')
           OR r.updated_at>=now()-sqlc.arg(retention_days)::integer * interval '1 day')) AS eligible
  FROM internal_mail_messages m WHERE m.namespace=sqlc.arg(namespace)
)
SELECT count(*) FILTER (WHERE payload_pruned_at IS NULL)::bigint AS retained_messages,
       count(*) FILTER (WHERE payload_pruned_at IS NOT NULL)::bigint AS pruned_messages,
       coalesce(sum(payload_bytes) FILTER (WHERE payload_pruned_at IS NULL),0)::bigint AS retained_bytes,
       count(*) FILTER (WHERE eligible)::bigint AS eligible_messages,
       coalesce(sum(payload_bytes) FILTER (WHERE eligible),0)::bigint AS eligible_bytes,
       coalesce(greatest(0,extract(epoch FROM now()-min(created_at) FILTER (WHERE eligible))),0)::float8 AS oldest_eligible_seconds
FROM inventory;
