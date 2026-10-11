-- name: CopyContentsIdentity :one
SELECT current_setting('server_version_num')::integer AS server_version,
 current_database()::text AS database_name,d.oid AS database_oid,
 current_user::text AS role_name,r.oid AS role_oid,session_user::text AS session_role,
 current_setting('transaction_read_only')::boolean AS read_only,
 current_setting('transaction_isolation')::text AS isolation,
 pg_catalog.pg_encoding_to_char(d.encoding)::text AS encoding
FROM pg_catalog.pg_database d,pg_catalog.pg_roles r
WHERE d.datname=current_database() AND r.rolname=current_user;

-- SET LOCAL has no persistent effects. row_security=off raises an error rather
-- than allowing a policy-filtered sample to masquerade as complete stored data.
-- name: ConfigureCopyContentsOutput :exec
SELECT pg_catalog.set_config('search_path','pg_catalog',true),
 pg_catalog.set_config('row_security','off',true),
 pg_catalog.set_config('TimeZone','UTC',true),
 pg_catalog.set_config('DateStyle','ISO, YMD',true),
 pg_catalog.set_config('IntervalStyle','postgres',true),
 pg_catalog.set_config('bytea_output','hex',true),
 pg_catalog.set_config('extra_float_digits','3',true),
 pg_catalog.set_config('lc_monetary','C',true),
 pg_catalog.set_config('xmloption','content',true),
 pg_catalog.set_config('enable_seqscan','on',true),
 pg_catalog.set_config('enable_indexscan','off',true),
 pg_catalog.set_config('enable_indexonlyscan','off',true),
 pg_catalog.set_config('enable_bitmapscan','off',true);

-- Physical indexes/TOAST and built-in catalogues are not independent logical
-- data sets. Every durable user stored relation, including extension-owned,
-- inherited, partitioned, closed/unpopulated MV and foreign relation, is input.
-- TOAST storage has relkind 't', intentionally absent from this list.
-- name: CopyContentsRelations :many
SELECT c.oid,n.nspname::text AS schema_name,c.relname::text AS relation_name,
 c.relkind::text AS kind,c.relpersistence::text AS persistence,c.relispopulated AS populated
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE c.relkind IN ('r','m','p','S','f') AND
 (c.oid>=16384::oid OR n.nspname NOT IN ('pg_catalog','pg_toast','information_schema'))
ORDER BY n.nspname::text COLLATE "C",c.relname::text COLLATE "C" LIMIT $1::integer;

-- name: CopyContentsTypes :many
SELECT t.oid,n.nspname::text AS schema_name,t.typname::text AS type_name,
 t.typtype::text AS kind,t.typcategory::text AS category,t.typelem AS element,
 t.typbasetype AS base,t.typrelid AS relation,pn.nspname::text AS output_schema,
 p.proname::text AS output_name,p.oid AS output_oid,coalesce(r.rngsubtype,0::oid)::oid AS range_subtype,
 coalesce((SELECT jsonb_agg(e.enumlabel::text ORDER BY e.enumsortorder) FROM pg_catalog.pg_enum e WHERE e.enumtypid=t.oid),'[]'::jsonb)::jsonb AS labels
FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace
 JOIN pg_catalog.pg_proc p ON p.oid=t.typoutput JOIN pg_catalog.pg_namespace pn ON pn.oid=p.pronamespace
 LEFT JOIN pg_catalog.pg_range r ON r.rngtypid=t.oid OR r.rngmultitypid=t.oid
ORDER BY t.oid LIMIT $1::integer;

-- Include fields of every composite too. Source attnum/OIDs guard SQL identity;
-- the comparison uses logical ordered column/type names, never destination OIDs.
-- name: CopyContentsColumns :many
SELECT a.attrelid,a.attname::text AS column_name,a.atttypid,a.atttypmod
FROM pg_catalog.pg_attribute a WHERE a.attnum>0 AND NOT a.attisdropped
ORDER BY a.attrelid,a.attnum LIMIT $1::integer;

-- Server quoting is the only source of dynamic COPY SQL. Direct type output
-- avoids application-defined casts to text. ONLY avoids double-counting children.
-- name: FormatCopyContentsRelation :one
SELECT CASE WHEN c.relkind='S' THEN
 pg_catalog.format('COPY (SELECT last_value, is_called FROM %I.%I) TO STDOUT (FORMAT text, ENCODING ''UTF8'')',n.nspname,c.relname)
 ELSE pg_catalog.format('COPY (SELECT * FROM ONLY %I.%I) TO STDOUT (FORMAT text, ENCODING ''UTF8'')',n.nspname,c.relname)
 END::text AS command
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE c.oid=$1::oid AND n.nspname::text=$2::text AND c.relname::text=$3::text AND c.relkind::text=$4::text;

-- Large-object APIs enforce object read permissions and stream beyond the bytea
-- size limit. Descriptors are transaction-owned, INV_READ is PostgreSQL's flag.
-- name: CopyContentsLargeObjects :many
SELECT oid FROM pg_catalog.pg_largeobject_metadata ORDER BY oid LIMIT $1::integer;
-- name: OpenCopyContentsLargeObject :one
SELECT pg_catalog.lo_open($1::oid,262144::integer)::integer;
-- name: ReadCopyContentsLargeObject :one
SELECT pg_catalog.loread($1::integer,$2::integer)::bytea;
-- name: CloseCopyContentsLargeObject :one
SELECT pg_catalog.lo_close($1::integer)::integer;
