-- name: LockLiveDatabase :one
SELECT d.id::text AS id FROM managed_postgres_databases d
 WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid AND d.deleted_at IS NULL
 FOR NO KEY UPDATE;

-- name: InsertLiveKey :exec
INSERT INTO managed_postgres_live_keys (kid,account_id,database_id,sealed_secret,recipient)
 VALUES (sqlc.arg(kid)::text,sqlc.arg(account_id)::text::uuid,sqlc.arg(database_id)::text::uuid,sqlc.arg(sealed_secret)::bytea,sqlc.arg(recipient)::text);

-- name: HasActiveLiveKey :one
SELECT EXISTS(SELECT 1 FROM managed_postgres_live_keys WHERE database_id=sqlc.arg(database_id)::text::uuid AND retires_at IS NULL)::boolean;

-- name: DeleteRetiringLiveKeys :exec
DELETE FROM managed_postgres_live_keys WHERE database_id=sqlc.arg(database_id)::text::uuid AND retires_at IS NOT NULL;

-- name: RetireActiveLiveKey :execrows
UPDATE managed_postgres_live_keys SET retires_at=clock_timestamp()+make_interval(secs=>sqlc.arg(overlap_seconds)::double precision)
 WHERE database_id=sqlc.arg(database_id)::text::uuid AND retires_at IS NULL;

-- name: DeleteActiveLiveKey :execrows
DELETE FROM managed_postgres_live_keys WHERE database_id=sqlc.arg(database_id)::text::uuid AND retires_at IS NULL;

-- name: DeleteLiveKeys :execrows
DELETE FROM managed_postgres_live_keys WHERE database_id=sqlc.arg(database_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ListLiveKeys :many
SELECT kid,created_at,retires_at FROM managed_postgres_live_keys
 WHERE database_id=sqlc.arg(database_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND (retires_at IS NULL OR retires_at>clock_timestamp())
 ORDER BY created_at DESC, kid;

-- name: LiveKeyRing :many
SELECT k.kid,k.sealed_secret,k.recipient,k.retires_at FROM managed_postgres_live_keys k
 JOIN managed_postgres_databases d ON d.id=k.database_id
 WHERE k.database_id=sqlc.arg(database_id)::text::uuid AND d.deleted_at IS NULL AND d.state NOT IN ('deleting','deleted')
 AND (k.retires_at IS NULL OR k.retires_at>clock_timestamp())
 ORDER BY k.created_at, k.kid;
