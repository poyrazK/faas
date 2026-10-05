-- Session ownership covers the top-level nontransactional CREATE DATABASE.
-- Use the same lock key as role/membership materialization to protect seed OIDs.
-- name: LockCopyDatabases :exec
SELECT pg_catalog.pg_advisory_lock(pg_catalog.hashtext('gregale copy role seed v1'),0);

-- name: UnlockCopyDatabases :one
SELECT pg_catalog.pg_advisory_unlock(pg_catalog.hashtext('gregale copy role seed v1'),0)::boolean;

-- name: CopyDatabaseSchemaExists :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='gregale_copy_databases')::boolean;

-- name: InstallCopyDatabaseSchema :exec
CREATE SCHEMA gregale_copy_databases;

-- name: InstallCopyDatabasePlan :exec
CREATE TABLE gregale_copy_databases.plan (
 singleton boolean PRIMARY KEY CHECK (singleton),
 version integer NOT NULL CHECK (version=1), body jsonb NOT NULL
);

-- name: InstallCopyDatabaseReceipts :exec
CREATE TABLE gregale_copy_databases.databases (
 source_oid oid PRIMARY KEY, target_oid oid NOT NULL UNIQUE,
 state text NOT NULL CHECK (state IN ('reserved','creating','created','existing')),
 claimed_at timestamptz, created_at timestamptz,
 CHECK ((state='reserved' AND claimed_at IS NULL AND created_at IS NULL) OR
   (state='creating' AND claimed_at IS NOT NULL AND created_at IS NULL) OR
   (state='created' AND claimed_at IS NOT NULL AND created_at IS NOT NULL) OR
   (state='existing' AND claimed_at IS NULL AND created_at IS NOT NULL))
);

-- name: PrivateCopyDatabaseJournal :one
SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname='gregale_copy_databases'
 AND n.nspowner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE a.grantee<>n.nspowner)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace=n.oid)
 AND (SELECT count(*) FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND c.relkind<>'i')=2
 AND (SELECT count(*) FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND c.relkind='r' AND c.relname IN ('plan','databases'))=2
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND
   (c.relowner<>n.nspowner OR c.relrowsecurity OR c.relforcerowsecurity OR
    EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a WHERE a.grantee<>c.relowner) OR
    EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t WHERE t.tgrelid=c.oid) OR
    EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite r WHERE r.ev_class=c.oid)))
 AND (SELECT pg_catalog.array_agg(a.attname::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='plan' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['singleton','version','body']::text[]
 AND (SELECT pg_catalog.array_agg(a.atttypid::regtype::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='plan' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['boolean','integer','jsonb']::text[]
 AND (SELECT pg_catalog.array_agg(a.attname::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='databases' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['source_oid','target_oid','state','claimed_at','created_at']::text[]
 AND (SELECT pg_catalog.array_agg(a.atttypid::regtype::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='databases' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['oid','oid','text','timestamp with time zone','timestamp with time zone']::text[]
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relkind='r' AND a.attnum>0 AND
   (a.attisdropped OR a.attgenerated<>'' OR (NOT a.attnotnull AND a.attname NOT IN ('claimed_at','created_at'))))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON c.oid=a.adrelid WHERE c.relnamespace=n.oid)
)::boolean;

-- name: CopyDatabasePlanBody :one
SELECT body FROM gregale_copy_databases.plan WHERE singleton AND version=1;

-- name: InsertCopyDatabasePlanBody :exec
INSERT INTO gregale_copy_databases.plan(singleton,version,body) VALUES (true,1,$1::jsonb);

-- name: CopyDatabaseReceipts :many
SELECT * FROM gregale_copy_databases.databases ORDER BY source_oid;

-- name: ReserveCopyDatabase :exec
INSERT INTO gregale_copy_databases.databases(source_oid,target_oid,state,created_at)
SELECT a.source_oid,a.target_oid,a.receipt_state,
 CASE WHEN a.receipt_state='existing' THEN clock_timestamp() ELSE NULL END
FROM (SELECT sqlc.arg(source_oid)::oid AS source_oid,
 sqlc.arg(target_oid)::oid AS target_oid,sqlc.arg(receipt_state)::text AS receipt_state) a;

-- name: ClaimCopyDatabase :execrows
UPDATE gregale_copy_databases.databases SET state='creating',claimed_at=clock_timestamp()
WHERE source_oid=$1::oid AND state='reserved';

-- name: CompleteCopyDatabase :execrows
UPDATE gregale_copy_databases.databases SET state='created',created_at=clock_timestamp()
WHERE source_oid=$1::oid AND state='creating';

-- CREATE DATABASE must execute at top level. This SQLC query is the only
-- statement generator; identifiers/literals are quoted by PostgreSQL format.
-- Parameters come exclusively from the validated immutable plan, pending claim
-- and pinned actor/tablespace identities. Go transmits the returned command whole.
-- Inheriting template0's pinned tablespace needs no extra CREATE grant; explicitly
-- naming even that same tablespace would impose an additional ACL precondition.
-- name: FormatCopyDatabaseCreate :one
SELECT pg_catalog.format(
 'CREATE DATABASE %I WITH OWNER %I TEMPLATE template0 ENCODING %s LC_COLLATE %L LC_CTYPE %L LOCALE_PROVIDER %s%s%s%s%s ALLOW_CONNECTIONS false IS_TEMPLATE false CONNECTION LIMIT %s OID %s',
 sqlc.arg(database_name)::text,sqlc.arg(owner_name)::text,sqlc.arg(encoding)::integer,
 sqlc.arg(collation_name)::text,sqlc.arg(ctype)::text,
 CASE WHEN sqlc.arg(locale_provider)::text='i' THEN 'icu' ELSE 'libc' END,
 CASE WHEN sqlc.narg(locale)::text IS NULL THEN '' ELSE pg_catalog.format(' ICU_LOCALE %L',sqlc.narg(locale)::text) END,
 CASE WHEN sqlc.narg(icu_rules)::text IS NULL THEN '' ELSE pg_catalog.format(' ICU_RULES %L',sqlc.narg(icu_rules)::text) END,
 CASE WHEN sqlc.narg(collation_version)::text IS NULL THEN '' ELSE pg_catalog.format(' COLLATION_VERSION %L',sqlc.narg(collation_version)::text) END,
 CASE WHEN (SELECT dattablespace FROM pg_catalog.pg_database WHERE datname='template0')=sqlc.arg(tablespace_oid)::oid
 THEN '' ELSE pg_catalog.format(' TABLESPACE %I',sqlc.arg(tablespace)::text) END,
 sqlc.arg(connection_limit)::integer,sqlc.arg(database_oid)::bigint)::text;
