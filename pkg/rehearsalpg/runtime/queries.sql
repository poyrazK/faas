-- name: BackendIdentity :one
SELECT (pg_catalog.to_jsonb(pg_catalog.pg_control_system())->>'system_identifier')::text AS system_id,
 (SELECT oid::text FROM pg_catalog.pg_database WHERE datname='gregale_rehearsal_admin')::text AS admin_database_id;

-- name: ClusterReady :one
SELECT (current_database()='gregale_rehearsal_admin'
 AND current_setting('server_version_num')::integer>=160000
 AND current_setting('max_prepared_transactions')::integer=0
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=current_user AND rolsuper)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname NOT IN ('gregale_rehearsal_admin','postgres','template0','template1') AND datname::text !~ '^grpg_[a-f0-9]{32}$'))::boolean;

-- name: FormatCreateRole :one
SELECT pg_catalog.format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT CONNECTION LIMIT 16',sqlc.arg(role_name)::text,sqlc.arg(password)::text)::text;
-- name: FormatCreateParticipant :one
SELECT pg_catalog.format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT CONNECTION LIMIT 16 IN ROLE %I',sqlc.arg(role_name)::text,sqlc.arg(password)::text,sqlc.arg(owner_role)::text)::text;
-- name: ParticipantRoles :many
SELECT r.rolname::text FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_auth_members m ON m.member=r.oid
 JOIN pg_catalog.pg_roles owner ON owner.oid=m.roleid
WHERE owner.rolname=sqlc.arg(owner_role)::text AND r.rolname::text ~ '^grpw_[a-f0-9]{40}$' ORDER BY r.rolname LIMIT 16;
-- name: FormatCreateDatabase :one
SELECT pg_catalog.format('CREATE DATABASE %I TEMPLATE template0 ENCODING ''UTF8''',sqlc.arg(database_name)::text)::text;
-- name: FormatConfigureDatabase :one
SELECT pg_catalog.format('REVOKE ALL ON DATABASE %I FROM PUBLIC; GRANT CONNECT ON DATABASE %I TO %I',sqlc.arg(database_name)::text,sqlc.arg(database_name)::text,sqlc.arg(role_name)::text)::text;
-- name: FormatConfigureSchema :one
SELECT pg_catalog.format('REVOKE ALL ON SCHEMA public FROM PUBLIC; GRANT USAGE,CREATE ON SCHEMA public TO %I',sqlc.arg(role_name)::text)::text;
-- name: FormatFreezeRole :one
SELECT pg_catalog.format('ALTER ROLE %I NOLOGIN',sqlc.arg(role_name)::text)::text;
-- name: FormatRotateRole :one
SELECT pg_catalog.format('ALTER ROLE %I PASSWORD %L',sqlc.arg(role_name)::text,sqlc.arg(password)::text)::text;
-- name: FormatFreezeDatabase :one
SELECT pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS false',sqlc.arg(database_name)::text)::text;
-- name: FormatDropDatabase :one
SELECT pg_catalog.format('DROP DATABASE IF EXISTS %I WITH (FORCE)',sqlc.arg(database_name)::text)::text;
-- name: FormatDropRole :one
SELECT pg_catalog.format('DROP ROLE IF EXISTS %I',sqlc.arg(role_name)::text)::text;

-- name: DatabaseExists :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=sqlc.arg(database_name)::text)::boolean;
-- name: RoleExists :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=sqlc.arg(role_name)::text)::boolean;
-- name: PendingSQL :one
SELECT count(*)::bigint FROM pg_catalog.pg_stat_activity WHERE datname=sqlc.arg(database_name)::text AND pid<>sqlc.arg(collector_pid)::integer
 AND (state IS DISTINCT FROM 'idle' OR xact_start IS NOT NULL);
-- name: TerminateSessions :many
SELECT pg_catalog.pg_terminate_backend(pid)::boolean FROM pg_catalog.pg_stat_activity WHERE datname=sqlc.arg(database_name)::text AND pid<>sqlc.arg(collector_pid)::integer;
-- name: OtherSessions :one
SELECT count(*)::bigint FROM pg_catalog.pg_stat_activity WHERE datname=sqlc.arg(database_name)::text AND pid<>sqlc.arg(collector_pid)::integer;
-- name: SampleLockWait :one
SELECT coalesce(max(greatest(0,extract(epoch FROM (clock_timestamp()-l.waitstart))*1000)),0)::bigint
FROM pg_catalog.pg_locks l JOIN pg_catalog.pg_stat_activity a ON a.pid=l.pid
WHERE a.datname=sqlc.arg(database_name)::text AND NOT l.granted;

-- name: SupportedExtensions :one
SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extname<>'plpgsql')::boolean;
-- name: Relations :many
SELECT n.nspname::text AS schema_name,c.relname::text AS table_name,c.relkind::text AS kind,c.relpersistence::text AS persistence
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname::text NOT LIKE 'pg_toast%'
 AND n.nspname::text NOT LIKE 'pg_temp_%' AND c.relkind IN ('r','p','v','m','f') ORDER BY n.nspname,c.relname LIMIT 17;
-- name: Columns :many
SELECT a.attname::text AS name,(t.oid<16384 AND tn.nspname='pg_catalog' AND pn.nspname='pg_catalog'
 AND t.typname IN ('bool','int2','int4','int8','float4','float8','numeric','text','varchar','bpchar','bytea','uuid','date','time','timetz','timestamp','timestamptz','interval','json','jsonb'))::boolean AS supported
FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace tn ON tn.oid=t.typnamespace
 JOIN pg_catalog.pg_proc p ON p.oid=t.typoutput JOIN pg_catalog.pg_namespace pn ON pn.oid=p.pronamespace
WHERE n.nspname=sqlc.arg(schema_name)::text AND c.relname=sqlc.arg(table_name)::text AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum;

-- Server quoting is the sole source of dynamic table SELECT SQL.
-- name: FormatSelect :one
SELECT pg_catalog.format('SELECT CASE WHEN octet_length(row::text)<=65536 THEN row ELSE NULL END FROM (SELECT pg_catalog.jsonb_build_object(%s) AS row FROM ONLY %I.%I LIMIT 1001) s',
 (SELECT string_agg(pg_catalog.format('%L, pg_catalog.to_jsonb(%I)',name,name),',' ORDER BY name) FROM unnest(sqlc.arg(columns)::text[]) AS name),
 sqlc.arg(schema_name)::text,sqlc.arg(table_name)::text)::text;

-- name: ConfigureCapture :exec
SELECT pg_catalog.set_config('search_path','pg_catalog',true),pg_catalog.set_config('row_security','off',true),
 pg_catalog.set_config('TimeZone','UTC',true),pg_catalog.set_config('DateStyle','ISO, YMD',true),
 pg_catalog.set_config('IntervalStyle','postgres',true),pg_catalog.set_config('bytea_output','hex',true),
 pg_catalog.set_config('extra_float_digits','3',true),pg_catalog.set_config('statement_timeout','5000',true),
 pg_catalog.set_config('enable_indexscan','off',true),pg_catalog.set_config('enable_indexonlyscan','off',true),pg_catalog.set_config('enable_bitmapscan','off',true);
