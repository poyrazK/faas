-- name: LockLiveDatabase :one
SELECT d.id::text AS id FROM managed_postgres_databases d
 WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid AND d.deleted_at IS NULL
 FOR NO KEY UPDATE;

-- name: InsertLiveKey :exec
INSERT INTO managed_postgres_live_keys (kid,account_id,database_id,sealed_secret,recipient,sealed_secret_sig)
 VALUES (sqlc.arg(kid)::text,sqlc.arg(account_id)::text::uuid,sqlc.arg(database_id)::text::uuid,sqlc.arg(sealed_secret)::bytea,sqlc.arg(recipient)::text,
 sqlc.narg(sealed_secret_sig)::bytea);

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
SELECT k.kid,k.sealed_secret,k.recipient,k.retires_at,k.sealed_secret_sig FROM managed_postgres_live_keys k
 JOIN managed_postgres_databases d ON d.id=k.database_id
 WHERE k.database_id=sqlc.arg(database_id)::text::uuid AND d.deleted_at IS NULL AND d.state NOT IN ('deleting','deleted')
 AND (k.retires_at IS NULL OR k.retires_at>clock_timestamp())
 ORDER BY k.created_at, k.kid;

-- name: AppLiveRoutes :many
-- Each live key of a database the app is bound to, with the logins live
-- queries read as there: sealed queries as the app's read-only (else
-- read-write) binding, RLS-mode reads as its Data API binding (ADR-650).
SELECT DISTINCT k.kid,d.id::text AS database_id,d.backend_id,d.backend_fingerprint,
 coalesce((SELECT x.provider_identity_id FROM managed_postgres_bindings x WHERE x.app_id=b.app_id AND x.database_id=d.id
   AND x.state IN ('ready','retiring') AND x.access IN ('read_only','read_write') AND x.provider_identity_id IS NOT NULL
   ORDER BY x.access='read_only' DESC, x.scope='default' DESC, x.created_at LIMIT 1),'')::text AS sealed_role,
 coalesce((SELECT x.provider_identity_id FROM managed_postgres_bindings x WHERE x.app_id=b.app_id AND x.database_id=d.id
   AND x.state IN ('ready','retiring') AND x.access='data_api' AND x.provider_identity_id IS NOT NULL
   ORDER BY x.scope='default' DESC, x.created_at LIMIT 1),'')::text AS data_api_role
 FROM managed_postgres_bindings b
 JOIN managed_postgres_databases d ON d.id=b.database_id AND d.account_id=b.account_id
 JOIN managed_postgres_live_keys k ON k.database_id=d.id
 WHERE b.app_id=sqlc.arg(app_id)::text::uuid AND b.account_id=sqlc.arg(account_id)::text::uuid AND b.state IN ('ready','retiring')
 AND d.deleted_at IS NULL AND d.state NOT IN ('deleting','deleted')
 AND (k.retires_at IS NULL OR k.retires_at>clock_timestamp())
 ORDER BY k.kid;

-- name: LiveDatabaseApps :many
SELECT DISTINCT b.app_id::text AS app_id FROM managed_postgres_bindings b
 WHERE b.database_id=sqlc.arg(database_id)::text::uuid AND b.state IN ('ready','retiring')
 ORDER BY 1;

-- name: UpsertLiveLogin :exec
INSERT INTO managed_postgres_live_logins (database_id,account_id,sealed_login,recipient,provider_identity_id,slot,awake,sealed_login_sig)
 VALUES (sqlc.arg(database_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(sealed_login)::bytea,sqlc.arg(recipient)::text,
 sqlc.arg(provider_identity_id)::text,sqlc.arg(slot)::text,sqlc.arg(awake)::text,sqlc.narg(sealed_login_sig)::bytea)
 ON CONFLICT (database_id) DO UPDATE SET sealed_login=EXCLUDED.sealed_login, recipient=EXCLUDED.recipient,
 provider_identity_id=EXCLUDED.provider_identity_id, slot=EXCLUDED.slot, awake=EXCLUDED.awake,
 sealed_login_sig=EXCLUDED.sealed_login_sig, created_at=now();

-- name: LiveLoginOf :many
SELECT provider_identity_id,slot FROM managed_postgres_live_logins
 WHERE database_id=sqlc.arg(database_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: DeleteLiveLogin :execrows
DELETE FROM managed_postgres_live_logins WHERE database_id=sqlc.arg(database_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: LiveDatabases :many
-- The live databases of the given backends that have a login: the databases
-- livequeryd serves there.
SELECT d.id::text AS database_id,d.backend_id,l.sealed_login,l.recipient,l.slot,l.awake,l.sealed_login_sig
 FROM managed_postgres_live_logins l
 JOIN managed_postgres_databases d ON d.id=l.database_id
 WHERE d.backend_id=ANY(sqlc.arg(backend_ids)::text[]) AND d.deleted_at IS NULL AND d.state NOT IN ('deleting','deleted')
 AND EXISTS (SELECT 1 FROM managed_postgres_live_keys k WHERE k.database_id=d.id AND (k.retires_at IS NULL OR k.retires_at>clock_timestamp()))
 ORDER BY d.id;

-- name: SealedLiveKeysAfter :many
-- One page of every live key in key-ID order, for the operator re-seal
-- (ADR-20261016151947 at-rest binding, release 2).
SELECT kid,database_id::text AS database_id,sealed_secret,recipient,sealed_secret_sig FROM managed_postgres_live_keys
 WHERE kid>sqlc.arg(after)::text ORDER BY kid LIMIT sqlc.arg(page_size)::int;

-- name: ResealLiveKey :execrows
-- Replaces a key's seal only while it still holds the one the re-seal read
-- and names the recipient the re-seal sealed to, so a key rotated, revoked
-- or redirected meanwhile is left as it is. The new ciphertext is unsigned
-- until the operator backfill signs it (ADR 20261028161502).
UPDATE managed_postgres_live_keys SET sealed_secret=sqlc.arg(sealed_secret)::bytea, sealed_secret_sig=NULL
 WHERE kid=sqlc.arg(kid)::text AND database_id=sqlc.arg(database_id)::text::uuid AND sealed_secret=sqlc.arg(previous)::bytea
   AND recipient=sqlc.arg(recipient)::text;

-- name: SealedLiveLoginsAfter :many
-- One page of every live login in database order, for the operator re-seal.
SELECT database_id::text AS database_id,sealed_login,recipient,sealed_login_sig FROM managed_postgres_live_logins
 WHERE database_id::text>sqlc.arg(after)::text ORDER BY database_id::text LIMIT sqlc.arg(page_size)::int;

-- name: SignLiveKey :execrows
-- The operator backfill signs an unsigned key as it stands (ADR
-- 20261028161502), only while it still holds the ciphertext and recipient
-- the backfill read.
UPDATE managed_postgres_live_keys SET sealed_secret_sig=sqlc.arg(sealed_secret_sig)::bytea
 WHERE kid=sqlc.arg(kid)::text AND database_id=sqlc.arg(database_id)::text::uuid AND sealed_secret=sqlc.arg(sealed_secret)::bytea
   AND recipient=sqlc.arg(recipient)::text AND sealed_secret_sig IS NULL;

-- name: SignLiveLogin :execrows
-- As SignLiveKey, for a login.
UPDATE managed_postgres_live_logins SET sealed_login_sig=sqlc.arg(sealed_login_sig)::bytea
 WHERE database_id=sqlc.arg(database_id)::text::uuid AND sealed_login=sqlc.arg(sealed_login)::bytea
   AND recipient=sqlc.arg(recipient)::text AND sealed_login_sig IS NULL;

-- name: DropLegacyLiveLogin :execrows
-- Removes a login the re-seal found under the legacy namespace, only while it
-- still holds the seal the re-seal read (amended 2026-10-28). apid then
-- issues the database a new login, sealed bound to it.
DELETE FROM managed_postgres_live_logins
 WHERE database_id=sqlc.arg(database_id)::text::uuid AND sealed_login=sqlc.arg(previous)::bytea
   AND recipient=sqlc.arg(recipient)::text;

-- name: LiveDatabasesWithoutLogin :many
-- Databases with an active live key and no login, the ones apid issues a
-- login for: a login the re-seal dropped, or one never stored.
SELECT DISTINCT k.database_id::text AS database_id,k.account_id::text AS account_id
 FROM managed_postgres_live_keys k
 JOIN managed_postgres_databases d ON d.id=k.database_id
 WHERE k.retires_at IS NULL AND d.deleted_at IS NULL AND d.state NOT IN ('deleting','deleted')
   AND NOT EXISTS (SELECT 1 FROM managed_postgres_live_logins l WHERE l.database_id=k.database_id)
 ORDER BY 1 LIMIT sqlc.arg(page_size)::int;
