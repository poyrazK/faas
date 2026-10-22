-- name: BackendIdentity :one
SELECT (pg_catalog.to_jsonb(pg_catalog.pg_control_system())->>'system_identifier')::text AS system_id,
 (SELECT oid::text FROM pg_catalog.pg_database WHERE datname='gregale_rehearsal_admin')::text AS admin_database_id;

-- name: LockFixtureDatabase :exec
SELECT pg_catalog.pg_advisory_lock(pg_catalog.hashtextextended('rehearsal-fixture:'||sqlc.arg(database_name)::text,0));

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
-- name: FormatRecoverDatabase :one
SELECT pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS true',sqlc.arg(database_name)::text)::text;
-- name: DatabaseConnectionsAllowed :one
SELECT datallowconn::boolean FROM pg_catalog.pg_database WHERE datname=sqlc.arg(database_name)::text;
-- name: FaultAccessProbe :one
SELECT current_database()=sqlc.arg(database_name)::text AND
 pg_catalog.has_database_privilege(sqlc.arg(role_name)::text,current_database(),'CONNECT') AND
 EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=sqlc.arg(role_name)::text AND rolcanlogin) AS ready;
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
 AND n.nspname::text NOT LIKE 'pg_temp_%' AND c.relkind IN ('r','p','v','m','f') ORDER BY n.nspname,c.relname LIMIT 65;

-- name: SchemaInventory :many
-- Every user-schema object a migration can create, with a definition that is
-- stable across two databases built from the same fixture: no OIDs or
-- database names (ADR-20261020180000). Table rows are counted separately.
WITH user_ns AS (
 SELECT oid,nspname FROM pg_catalog.pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema')
  AND nspname::text NOT LIKE 'pg_toast%' AND nspname::text NOT LIKE 'pg_temp_%'
)
SELECT o.kind::text AS kind,o.name::text AS name,o.definition::text AS definition,o.rel_schema::text AS rel_schema,o.rel_name::text AS rel_name FROM (
 SELECT CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned_table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized_view'
   WHEN 'f' THEN 'foreign_table' ELSE 'sequence' END AS kind,
  n.nspname||'.'||c.relname AS name,
  CASE WHEN c.relkind IN ('v','m') THEN pg_catalog.pg_get_viewdef(c.oid)
   WHEN c.relkind='S' THEN coalesce((SELECT pg_catalog.format('%s start %s increment %s',pg_catalog.format_type(s.seqtypid,NULL),s.seqstart,s.seqincrement)
    FROM pg_catalog.pg_sequence s WHERE s.seqrelid=c.oid),'')
   ELSE coalesce((SELECT string_agg(pg_catalog.format('%I %s%s%s',a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod),
     CASE WHEN a.attnotnull THEN ' not null' ELSE '' END,coalesce(' default '||pg_catalog.pg_get_expr(d.adbin,d.adrelid),'')),', ' ORDER BY a.attnum)
    FROM pg_catalog.pg_attribute a LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
    WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped),'')||CASE WHEN c.relpersistence='u' THEN ' unlogged' ELSE '' END END AS definition,
  CASE WHEN c.relkind='r' THEN n.nspname ELSE '' END AS rel_schema,CASE WHEN c.relkind='r' THEN c.relname ELSE '' END AS rel_name
 FROM pg_catalog.pg_class c JOIN user_ns n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p','v','m','f','S')
 UNION ALL
 SELECT 'index',n.nspname||'.'||i.relname,pg_catalog.pg_get_indexdef(x.indexrelid),'',''
 FROM pg_catalog.pg_index x JOIN pg_catalog.pg_class i ON i.oid=x.indexrelid JOIN user_ns n ON n.oid=i.relnamespace
 UNION ALL
 SELECT 'constraint',n.nspname||'.'||t.relname||'.'||c.conname,pg_catalog.pg_get_constraintdef(c.oid),'',''
 FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN user_ns n ON n.oid=t.relnamespace
 UNION ALL
 SELECT 'type',n.nspname||'.'||t.typname,CASE t.typtype
   WHEN 'e' THEN 'enum ('||coalesce((SELECT string_agg(pg_catalog.quote_literal(e.enumlabel),', ' ORDER BY e.enumsortorder) FROM pg_catalog.pg_enum e WHERE e.enumtypid=t.oid),'')||')'
   WHEN 'd' THEN 'domain '||pg_catalog.format_type(t.typbasetype,t.typtypmod)||coalesce(' default '||t.typdefault,'')
   ELSE 'type '||t.typtype::text END,'',''
 FROM pg_catalog.pg_type t JOIN user_ns n ON n.oid=t.typnamespace
 WHERE t.typtype IN ('e','d','r','m') OR t.typtype='c' AND EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.oid=t.typrelid AND c.relkind='c')
 UNION ALL
 SELECT 'function',n.nspname||'.'||p.proname||'('||pg_catalog.pg_get_function_identity_arguments(p.oid)||')',
  'sha256:'||pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(pg_catalog.pg_get_functiondef(p.oid),'UTF8')),'hex'),'',''
 FROM pg_catalog.pg_proc p JOIN user_ns n ON n.oid=p.pronamespace WHERE p.prokind IN ('f','p')
 UNION ALL
 SELECT 'trigger',n.nspname||'.'||c.relname||'.'||t.tgname,pg_catalog.pg_get_triggerdef(t.oid),'',''
 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN user_ns n ON n.oid=c.relnamespace WHERE NOT t.tgisinternal
) o ORDER BY o.kind,o.name LIMIT 257;

-- name: FormatCount :one
-- Server quoting builds the bounded row count of one undeclared table.
SELECT pg_catalog.format('SELECT count(*) FROM (SELECT 1 FROM ONLY %I.%I LIMIT 10001) s',
 sqlc.arg(schema_name)::text,sqlc.arg(table_name)::text)::text;
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
