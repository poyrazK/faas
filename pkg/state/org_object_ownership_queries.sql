-- name: ObjectStorageLockOwner :one
WITH locked_org AS MATERIALIZED (
 SELECT o.id FROM object_buckets b JOIN orgs o ON o.id=b.org_id
 WHERE b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid AND NOT o.personal_org FOR UPDATE OF o
)
SELECT coalesce(l.id,o.id)::uuid AS id, object_storage_usage_subject(b.org_id,b.account_id)::uuid AS subject
FROM object_buckets b JOIN orgs o ON o.id=b.org_id LEFT JOIN locked_org l ON l.id=o.id
WHERE b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid
AND (CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.status IN ('active','past_due') AND o.abuse_hold_at IS NULL
ELSE EXISTS(SELECT 1 FROM accounts a WHERE a.id=b.account_id AND a.status IN ('active','past_due') AND a.abuse_hold_at IS NULL) END);

-- name: ObjectStorageReserveOwner :one
WITH locked_org AS MATERIALIZED (
 SELECT o.id FROM apps a JOIN orgs o ON o.id=a.org_id
 WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND NOT o.personal_org FOR UPDATE OF o
)
SELECT coalesce(l.id,o.id)::uuid AS id,o.personal_org,o.resource_ownership_enabled,o.status
FROM apps a JOIN orgs o ON o.id=a.org_id LEFT JOIN locked_org l ON l.id=o.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
AND NOT o.deleted_pending AND o.status='active';

-- name: ObjectStoragePersonalReference :one
SELECT EXISTS(SELECT 1 FROM object_buckets b JOIN orgs o ON o.id=b.org_id
WHERE b.id=$1 AND b.account_id=$2 AND o.personal_org)::boolean AS personal;

-- name: ObjectStoragePersonalCredentialReference :one
SELECT EXISTS(SELECT 1 FROM object_storage_s3_credentials c JOIN object_buckets b ON b.id=c.bucket_id
JOIN orgs o ON o.id=b.org_id WHERE c.id=$1 AND b.account_id=$2 AND o.personal_org)::boolean AS personal;

-- name: ObjectStorageLockScope :one
WITH locked_org AS MATERIALIZED (
 SELECT o.id FROM object_buckets b JOIN orgs o ON o.id=b.org_id
 WHERE b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid AND NOT o.personal_org FOR UPDATE OF o
)
SELECT coalesce(l.id,o.id)::uuid AS id FROM object_buckets b JOIN orgs o ON o.id=b.org_id LEFT JOIN locked_org l ON l.id=o.id
WHERE b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid;

-- name: ObjectStorageLockLegacyIdentity :exec
SELECT id FROM accounts WHERE id=$1 FOR UPDATE;

-- name: ObjectStorageSubject :one
SELECT object_storage_usage_subject(b.org_id,b.account_id)::uuid AS subject
FROM object_buckets b WHERE b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid;

-- name: ObjectStorageLockReportingOwners :many
SELECT o.id FROM orgs o WHERE NOT o.personal_org AND (o.id=sqlc.arg(subject)::uuid OR EXISTS
(SELECT 1 FROM object_buckets b WHERE b.org_id=o.id AND b.account_id=sqlc.arg(subject)::uuid))
ORDER BY o.id FOR UPDATE OF o;

-- name: ObjectStorageReportCutover :one
SELECT max(o.object_storage_owned_from)::timestamptz AS cutover FROM orgs o
WHERE (o.id=sqlc.arg(subject)::uuid OR EXISTS
(SELECT 1 FROM object_buckets b WHERE b.org_id=o.id AND b.account_id=sqlc.arg(subject)::uuid))
AND date_trunc('month',o.object_storage_owned_from AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'=sqlc.arg(period)::timestamptz;

-- name: ObjectStoragePurgeIdentityEventOutbox :exec
DELETE FROM event_fanout_outbox e WHERE e.account_id=$1 AND NOT
(e.source='gregale.storage' AND EXISTS(SELECT 1 FROM object_buckets b JOIN orgs o ON o.id=b.org_id
 WHERE b.account_id=e.account_id AND b.id::text=e.event_data->>'bucket_id' AND b.app_id::text=e.event_data->>'app_id'
 AND NOT o.personal_org AND o.resource_ownership_enabled));

-- name: ObjectStoragePurgeIdentityEventLog :exec
DELETE FROM events e WHERE (e.subject=$1::uuid OR e.data->>'account_id'=$1::text) AND NOT
(e.kind='event.published' AND e.data->>'source'='gregale.storage' AND EXISTS
 (SELECT 1 FROM object_buckets b JOIN orgs o ON o.id=b.org_id WHERE b.account_id=$1::uuid
 AND b.id::text=e.data->'data'->>'bucket_id' AND b.app_id::text=e.data->'data'->>'app_id'
 AND NOT o.personal_org AND o.resource_ownership_enabled));
