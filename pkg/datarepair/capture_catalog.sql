-- Embedded client probe, never installed or executed as a repair function.
WITH input AS (SELECT $1::text AS protocol),
 functions(name,compatible,executable) AS (__FUNCTIONS_SQL__),
 columns(name,compatible) AS (__INSTALLATION_SQL__),
 journal(name,compatible) AS (__INTERNAL_TRIGGERS_SQL__),
 grants(object,granted) AS (__PERMISSIONS_SQL__),
 receipt AS (
  SELECT c.*,n.nspname FROM input i LEFT JOIN pg_namespace n ON n.nspname='public'
  LEFT JOIN pg_class c ON c.relnamespace=n.oid AND c.relname=CASE i.protocol
   WHEN 'managed' THEN 'gregale_operation_inbox' WHEN 'customer' THEN 'gregale_customer_operation_inbox' END
 ), receipt_shape AS (
  SELECT coalesce(bool_and(r.oid IS NOT NULL AND r.relkind='r' AND NOT r.relispartition AND NOT r.relrowsecurity AND NOT r.relforcerowsecurity AND
   NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=r.oid OR inhparent=r.oid) AND
   NOT EXISTS(SELECT 1 FROM pg_rewrite WHERE ev_class=r.oid) AND
   NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=r.oid AND NOT tgisinternal) AND
   a.attnum>0 AND NOT a.attisdropped AND format_type(a.atttypid,a.atttypmod)=e.type),false) AND
   EXISTS(SELECT 1 FROM receipt r JOIN pg_index x ON x.indrelid=r.oid AND x.indisprimary AND x.indisvalid AND x.indnkeyatts=1
    JOIN pg_attribute a ON a.attrelid=r.oid AND a.attnum=x.indkey[0] AND a.attname='operation_id') AS ok
  FROM receipt r CROSS JOIN (VALUES ('operation_id','uuid'),('account_id','uuid'),('app_id','uuid'),('platform_tenant_id','uuid'),
   ('request_digest','bytea'),('response_body','text'),('created_at','timestamp with time zone')) e(name,type)
  LEFT JOIN pg_attribute a ON a.attrelid=r.oid AND a.attname=e.name
 ), facts AS (
  SELECT current_setting('server_version_num')::int BETWEEN 160000 AND 169999 AS pg16,
   EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_repair') AS installed,
   coalesce((SELECT bool_and(compatible) FROM columns),false) AND coalesce((SELECT bool_and(compatible) FROM journal),false) AS shape,
   coalesce((SELECT bool_and(compatible) FROM functions),false) AS functions_ok,
   coalesce((SELECT bool_and(granted) FROM grants WHERE object IN ('gregale_repair: USAGE','gregale_repair.enrollments: SELECT')),false) AS registry,
   ARRAY(SELECT DISTINCT 'gregale_repair.'||name FROM (SELECT name FROM columns WHERE NOT compatible UNION ALL SELECT name FROM journal WHERE NOT compatible) x ORDER BY 1) AS bad_tables,
   ARRAY(SELECT 'gregale_repair.'||name FROM functions WHERE NOT compatible ORDER BY name) AS bad_functions,
   ARRAY(SELECT DISTINCT object FROM (
    SELECT object FROM grants WHERE NOT granted AND object NOT LIKE 'gregale_repair.plans:%'
    UNION ALL SELECT 'gregale_repair.'||name||': EXECUTE' FROM functions WHERE NOT executable
    UNION ALL SELECT 'public: USAGE' FROM pg_namespace n WHERE n.nspname='public' AND NOT has_schema_privilege(n.oid,'USAGE')
    UNION ALL SELECT 'public.'||r.relname||': '||p.privilege FROM receipt r CROSS JOIN (VALUES ('SELECT'),('INSERT')) p(privilege)
     WHERE r.oid IS NOT NULL AND NOT has_table_privilege(r.oid,p.privilege)
    UNION ALL SELECT signature||': EXECUTE' FROM (VALUES ('pg_catalog.pg_advisory_xact_lock(bigint)'),
     ('pg_catalog.pg_advisory_xact_lock_shared(bigint)'),('pg_catalog.hashtextextended(text,bigint)')) p(signature)
     WHERE NOT has_function_privilege(to_regprocedure(signature),'EXECUTE')
   ) x ORDER BY object) AS missing_grants,
   ARRAY(SELECT object FROM (VALUES
    ('session_replication_role: origin required',current_setting('session_replication_role')='origin'),
    ('default_transaction_read_only: off required',current_setting('default_transaction_read_only')='off'),
    ('server: primary required',NOT pg_is_in_recovery()),
    ('gregale.repair_operation: empty required',nullif(current_setting('gregale.repair_operation',true),'') IS NULL),
    ('transaction: read-only diagnostic snapshot required',current_setting('transaction_read_only')='on')
   ) s(object,ok) WHERE NOT ok ORDER BY object) AS bad_session
 )
SELECT jsonb_build_object('inspect_enrollment',f.pg16 AND f.installed AND f.shape AND f.registry,
 'findings',jsonb_build_array(
  jsonb_build_object('id','connection','code','writer_connected','objects','[]'::jsonb),
  jsonb_build_object('id','postgres','code',CASE WHEN f.pg16 THEN 'postgres16' ELSE 'postgres_unsupported' END,'objects','[]'::jsonb),
  jsonb_build_object('id','installation','code',CASE WHEN NOT f.installed THEN 'schema_missing' WHEN f.shape THEN 'schema_current' ELSE 'schema_incompatible' END,'objects',to_jsonb(f.bad_tables)),
  jsonb_build_object('id','functions','code',CASE WHEN NOT f.installed THEN 'not_inspected' WHEN f.functions_ok THEN 'capture_functions_current' ELSE 'capture_functions_incompatible' END,'objects',CASE WHEN f.installed THEN to_jsonb(f.bad_functions) ELSE '[]'::jsonb END),
  jsonb_build_object('id','receipt','code',CASE WHEN r.ok THEN 'receipt_ready' ELSE 'receipt_incompatible' END,'objects',CASE WHEN r.ok THEN '[]'::jsonb ELSE jsonb_build_array(CASE i.protocol WHEN 'managed' THEN 'public.gregale_operation_inbox' ELSE 'public.gregale_customer_operation_inbox' END) END),
  jsonb_build_object('id','session','code',CASE WHEN cardinality(f.bad_session)=0 THEN 'capture_session_valid' ELSE 'capture_session_invalid' END,'objects',to_jsonb(f.bad_session)),
  jsonb_build_object('id','permissions','code',CASE WHEN NOT f.installed THEN 'not_inspected' WHEN cardinality(f.missing_grants)=0 THEN 'writer_permissions_present' ELSE 'writer_permissions_missing' END,'objects',CASE WHEN f.installed THEN to_jsonb(f.missing_grants) ELSE '[]'::jsonb END)
 )) AS probe
FROM facts f CROSS JOIN receipt_shape r CROSS JOIN input i
