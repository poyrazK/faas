-- name: ProducerLifecycleSchema :exec
SELECT a.namespace,a.producer_id,a.revision,a.suspended,a.active_credential_id,a.request_id,a.updated_at,
       c.credential_id,c.retires_at,c.revoked_at,c.created_at
FROM internal_mail_producer_access a LEFT JOIN internal_mail_producer_credentials c
ON c.namespace=a.namespace AND c.producer_id=a.producer_id LIMIT 0;

-- Shared access fences first insertion as well as later changes. Mutations
-- never acquire admission, delivery, recipient or event-consumer locks.
-- name: ShareProducerAccess :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended(sqlc.arg(namespace)::text||':'||sqlc.arg(producer_id)::text,871736));

-- name: LockProducerAccess :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text||':'||sqlc.arg(producer_id)::text,871736));

-- Actual database time is read after any advisory/row lock waits. The empty
-- credential ID denotes a trusted owner call; it never bypasses suspension.
-- name: CheckProducerAccess :one
SELECT EXISTS (SELECT 1 FROM internal_mail_producer_access a
    WHERE a.namespace=sqlc.arg(namespace) AND a.producer_id=sqlc.arg(producer_id) AND a.suspended) AS suspended,
    (sqlc.arg(credential_id)::text='' OR (
      NOT EXISTS (SELECT 1 FROM internal_mail_producer_credentials c WHERE c.namespace=sqlc.arg(namespace)
        AND c.producer_id=sqlc.arg(producer_id) AND c.credential_id=sqlc.arg(credential_id)
        AND (c.revoked_at IS NOT NULL OR c.retires_at<=clock_timestamp()))
      AND (sqlc.arg(credential_id)=coalesce(nullif((SELECT a.active_credential_id FROM internal_mail_producer_access a
        WHERE a.namespace=sqlc.arg(namespace) AND a.producer_id=sqlc.arg(producer_id)),''),sqlc.arg(primary_id)::text)
        OR EXISTS (SELECT 1 FROM internal_mail_producer_credentials c WHERE c.namespace=sqlc.arg(namespace)
          AND c.producer_id=sqlc.arg(producer_id) AND c.credential_id=sqlc.arg(credential_id)
          AND c.retires_at>clock_timestamp()))))::boolean AS credential_allowed;

-- name: ProducerAccess :one
SELECT coalesce((SELECT a.revision FROM internal_mail_producer_access a WHERE a.namespace=$1 AND a.producer_id=$2),0)::bigint AS revision,
       coalesce((SELECT a.suspended FROM internal_mail_producer_access a WHERE a.namespace=$1 AND a.producer_id=$2),false)::boolean AS suspended,
       coalesce((SELECT a.active_credential_id FROM internal_mail_producer_access a WHERE a.namespace=$1 AND a.producer_id=$2),'')::text AS active_credential_id,
       (SELECT a.updated_at FROM internal_mail_producer_access a WHERE a.namespace=$1 AND a.producer_id=$2)::timestamptz AS updated_at;

-- name: EnsureProducerAccess :exec
INSERT INTO internal_mail_producer_access(namespace,producer_id) VALUES($1,$2) ON CONFLICT DO NOTHING;

-- name: SetProducerAccess :exec
UPDATE internal_mail_producer_access SET revision=revision+1,suspended=$3,active_credential_id=$4,request_id=$5,updated_at=clock_timestamp()
WHERE namespace=$1 AND producer_id=$2;

-- name: ProducerCredential :one
SELECT * FROM internal_mail_producer_credentials WHERE namespace=$1 AND producer_id=$2 AND credential_id=$3;

-- name: EnsureProducerCredential :exec
INSERT INTO internal_mail_producer_credentials(namespace,producer_id,credential_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING;

-- Retirement is irreversible, including an operator changing private config.
-- A saved deadline can only get earlier, and a revoked key cannot be restored.
-- name: RetireProducerCredential :exec
UPDATE internal_mail_producer_credentials SET retires_at=least(retires_at,clock_timestamp()+sqlc.arg(overlap_seconds)::integer*interval '1 second')
WHERE namespace=sqlc.arg(namespace) AND producer_id=sqlc.arg(producer_id) AND credential_id=sqlc.arg(credential_id);

-- name: RevokeProducerCredential :exec
UPDATE internal_mail_producer_credentials SET revoked_at=coalesce(revoked_at,clock_timestamp())
WHERE namespace=$1 AND producer_id=$2 AND credential_id=$3;

-- name: ProducerLifecycleStatistics :one
SELECT (SELECT count(*) FROM internal_mail_producer_access a WHERE a.namespace=$1 AND a.suspended)::bigint AS suspended_producers,
       (SELECT count(*) FROM internal_mail_producer_credentials c WHERE c.namespace=$1 AND c.revoked_at IS NOT NULL)::bigint AS revoked_credentials,
       (SELECT count(*) FROM internal_mail_producer_credentials c WHERE c.namespace=$1 AND c.revoked_at IS NULL AND c.retires_at>clock_timestamp())::bigint AS retiring_credentials,
       (SELECT count(*) FROM internal_mail_producer_credentials c WHERE c.namespace=$1 AND c.revoked_at IS NULL AND c.retires_at<=clock_timestamp())::bigint AS expired_credentials;
