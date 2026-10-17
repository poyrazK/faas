-- Operator projections deliberately never fetch ciphertext, signing keys,
-- fingerprints, idempotency keys or lease tokens. Cursors use ascending IDs.

-- name: OperationsSchema :exec
SELECT id FROM internal_mail_operations LIMIT 0;

-- name: OperatorMessages :many
SELECT m.id,m.domain_id,m.created_at,m.payload_pruned_at,m.producer_id,m.sender_address,m.send_at,m.expires_at,
    (SELECT count(*) FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id)::bigint AS recipient_count
FROM internal_mail_messages m
WHERE m.namespace=sqlc.arg(namespace) AND m.id>sqlc.arg(after_id)::uuid
  AND (sqlc.arg(status)::text='' OR EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=m.namespace AND r.message_id=m.id AND r.status=sqlc.arg(status)))
ORDER BY m.id LIMIT sqlc.arg(page_limit);

-- name: OperatorMessage :one
SELECT id,domain_id,created_at,payload_pruned_at,producer_id,sender_address,send_at,expires_at FROM internal_mail_messages WHERE namespace=$1 AND id=$2;

-- name: OperatorRecipients :many
SELECT id,message_id,address,status,attempts,code,updated_at,next_attempt_at,lease_until,signing_selector
FROM internal_mail_recipients WHERE namespace=$1 AND message_id=$2 ORDER BY id;

-- name: OperatorEvents :many
SELECT e.id,e.recipient_id,e.kind,e.code,e.created_at FROM internal_mail_events e
JOIN internal_mail_recipients r ON r.namespace=e.namespace AND r.id=e.recipient_id
WHERE e.namespace=sqlc.arg(namespace) AND r.message_id=sqlc.arg(message_id) AND e.id>sqlc.arg(after_id)::text
ORDER BY e.id LIMIT sqlc.arg(page_limit);

-- name: OperatorDomains :many
SELECT id,name,status,selector,verification_token,public_key,created_at,dkim_key_id,authorization_state,last_checked_at,last_verified_at,verified_sending_ip,check_result,failure_count,first_failure_at,dns_revision FROM internal_mail_domains
WHERE namespace=sqlc.arg(namespace) AND id>sqlc.arg(after_id)::uuid ORDER BY id LIMIT sqlc.arg(page_limit);

-- name: OperatorDomain :one
SELECT id,name,status,selector,verification_token,public_key,created_at,dkim_key_id,authorization_state,last_checked_at,last_verified_at,verified_sending_ip,check_result,failure_count,first_failure_at,dns_revision FROM internal_mail_domains WHERE namespace=$1 AND id=$2;

-- name: OperatorSuppressions :many
SELECT address,reason,created_at FROM internal_mail_suppressions
WHERE namespace=sqlc.arg(namespace) AND address>sqlc.arg(after_id)::text ORDER BY address LIMIT sqlc.arg(page_limit);

-- name: LockOperation :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text || ':' || sqlc.arg(request_id)::text, 871727));

-- name: Operation :one
SELECT * FROM internal_mail_operations WHERE namespace=$1 AND id=$2;

-- name: InsertOperation :one
INSERT INTO internal_mail_operations(namespace,id,action,resource,actor,reason,evidence,request_hash,result)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: OperatorAudit :many
SELECT id,action,resource,actor,reason,evidence,result,created_at FROM internal_mail_operations
WHERE namespace=sqlc.arg(namespace) AND id>sqlc.arg(after_id)::uuid ORDER BY id LIMIT sqlc.arg(page_limit);
