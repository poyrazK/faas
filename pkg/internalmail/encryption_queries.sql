-- name: EncryptionSchema :exec
SELECT s.active_key_id,k.retired_at FROM internal_mail_encryption_state s,internal_mail_encryption_keys k LIMIT 0;

-- Namespace key fence (871737): writers take shared before any row/owner lock;
-- activation, migration and retirement take exclusive before observing refs.
-- name: ShareEncryption :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended($1::text,871737));

-- name: LockEncryption :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text,871737));

-- name: EncryptionState :one
SELECT * FROM internal_mail_encryption_state WHERE namespace=$1;

-- name: SelectEncryptionKey :one
INSERT INTO internal_mail_encryption_state(namespace,active_key_id,legacy_key_id,revision)
VALUES($1,$2,$3,1)
ON CONFLICT(namespace) DO UPDATE SET active_key_id=excluded.active_key_id,
    revision=internal_mail_encryption_state.revision+1,updated_at=clock_timestamp()
RETURNING *;

-- name: AdvanceEncryptionRevision :one
UPDATE internal_mail_encryption_state SET revision=revision+1,updated_at=clock_timestamp()
WHERE namespace=$1 RETURNING *;

-- name: EncryptionKeyRetired :one
SELECT EXISTS(SELECT 1 FROM internal_mail_encryption_keys WHERE namespace=$1 AND key_id=$2);

-- name: RetireEncryptionKey :exec
INSERT INTO internal_mail_encryption_keys(namespace,key_id) VALUES($1,$2) ON CONFLICT DO NOTHING;

-- name: ConfiguredEncryptionRetirements :many
SELECT key_id,retired_at FROM internal_mail_encryption_keys
WHERE namespace=sqlc.arg(namespace) AND key_id=ANY(sqlc.arg(key_ids)::text[]) ORDER BY key_id;

-- Legacy markers resolve through the durably pinned identity, never whichever
-- key happened to become active most recently. Fingerprints survive pruning.
-- name: EncryptionInventory :many
WITH blobs AS (
    SELECT 'payload'::text AS kind,m.sealed_payload AS sealed FROM internal_mail_messages m
        WHERE m.namespace=sqlc.arg(namespace) AND m.payload_pruned_at IS NULL AND octet_length(m.sealed_payload)>0
    UNION ALL
    SELECT 'domain',d.sealed_key FROM internal_mail_domains d WHERE d.namespace=sqlc.arg(namespace) AND octet_length(d.sealed_key)>0
    UNION ALL
    SELECT 'dkim',k.sealed_key FROM internal_mail_dkim_keys k WHERE k.namespace=sqlc.arg(namespace) AND octet_length(k.sealed_key)>0
), refs AS (
    SELECT CASE WHEN substring(b.sealed from 1 for 7)=convert_to('GMAIL2:','UTF8')
        THEN encode(substring(b.sealed from 8 for 64),'escape') ELSE sqlc.arg(legacy_key_id)::text END AS key_id,b.kind
    FROM blobs b
    UNION ALL
    SELECT CASE WHEN octet_length(m.payload_fingerprint)=32 THEN sqlc.arg(legacy_key_id)::text
        ELSE encode(substring(m.payload_fingerprint from 7 for 64),'escape') END,
        CASE WHEN m.payload_pruned_at IS NULL THEN 'fingerprint' ELSE 'pruned_fingerprint' END
    FROM internal_mail_messages m WHERE m.namespace=sqlc.arg(namespace) AND m.payload_fingerprint IS NOT NULL
)
SELECT key_id::text,
    count(*) FILTER (WHERE kind='payload')::bigint AS payloads,
    count(*) FILTER (WHERE kind='domain')::bigint AS domain_keys,
    count(*) FILTER (WHERE kind='dkim')::bigint AS dkim_keys,
    count(*) FILTER (WHERE kind IN ('fingerprint','pruned_fingerprint'))::bigint AS fingerprints,
    count(*) FILTER (WHERE kind='pruned_fingerprint')::bigint AS pruned_fingerprints
FROM refs GROUP BY key_id ORDER BY key_id LIMIT sqlc.arg(key_limit)::integer;

-- Called under the namespace key fence. Apply takes exclusive, so no writer,
-- pruning pass or DKIM selection/retirement can change these blobs mid-batch.
-- name: EncryptionCandidates :many
WITH candidates AS (
    SELECT 'domain'::text AS kind,d.id,d.id AS domain_id,(d.dkim_key_id IS NULL) AS legacy,
        coalesce(d.dkim_key_id,d.id) AS resource_id,d.sealed_key AS sealed,NULL::bytea AS fingerprint
    FROM internal_mail_domains d WHERE d.namespace=sqlc.arg(namespace) AND octet_length(d.sealed_key)>0
        AND substring(d.sealed_key from 1 for 72)<>convert_to('GMAIL2:'||sqlc.arg(active_key_id)::text||':','UTF8')
    UNION ALL
    SELECT 'dkim',k.id,k.domain_id,k.legacy,k.id,k.sealed_key,NULL::bytea FROM internal_mail_dkim_keys k
    WHERE k.namespace=sqlc.arg(namespace) AND octet_length(k.sealed_key)>0
        AND substring(k.sealed_key from 1 for 72)<>convert_to('GMAIL2:'||sqlc.arg(active_key_id)::text||':','UTF8')
    UNION ALL
    SELECT 'payload',m.id,m.domain_id,false,m.id,m.sealed_payload,m.payload_fingerprint FROM internal_mail_messages m
    WHERE m.namespace=sqlc.arg(namespace) AND m.payload_pruned_at IS NULL AND octet_length(m.sealed_payload)>0
        AND (substring(m.sealed_payload from 1 for 72)<>convert_to('GMAIL2:'||sqlc.arg(active_key_id)::text||':','UTF8')
            OR m.payload_fingerprint IS NULL OR octet_length(m.payload_fingerprint)=32
            OR substring(m.payload_fingerprint from 1 for 71)<>convert_to('GMFP2:'||sqlc.arg(active_key_id)::text||':','UTF8'))
)
SELECT kind::text,id,domain_id,legacy::boolean,resource_id::uuid,sealed::bytea,fingerprint::bytea
FROM candidates ORDER BY kind,id LIMIT sqlc.arg(batch_size)::integer;

-- Encryption migration deliberately changes no recipient, selector, DNS,
-- scheduling, event, ownership, quota or custody fields.
-- name: RewrapDomainKey :execrows
UPDATE internal_mail_domains SET sealed_key=$3 WHERE namespace=$1 AND id=$2;

-- name: RewrapDKIMKey :execrows
UPDATE internal_mail_dkim_keys SET sealed_key=$3 WHERE namespace=$1 AND id=$2 AND state<>'retired';

-- name: RewrapMessage :execrows
UPDATE internal_mail_messages SET sealed_payload=$3,payload_fingerprint=$4
WHERE namespace=$1 AND id=$2 AND payload_pruned_at IS NULL;
