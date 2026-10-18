WITH entry AS MATERIALIZED (
 SELECT e.*,c.oid AS live_oid,c.relnamespace,c.relname,n.nspname,
  coalesce(c.relkind='r' AND NOT c.relispartition AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND
   format('%I.%I',n.nspname,c.relname)=e.qualified_name AND
   NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid) AND
   cardinality(e.fields)>0 AND cardinality(e.keys)>0 AND cardinality(e.fields)+cardinality(e.keys)<=__MAX_OBJECTS__ AND
   cardinality(e.fields||e.keys)=(SELECT count(DISTINCT f) FROM unnest(e.fields||e.keys) f) AND
   NOT EXISTS(SELECT 1 FROM unnest(e.fields||e.keys) f LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname=f AND a.attnum>0 AND NOT a.attisdropped
    WHERE a.attnum IS NULL OR a.attgenerated<>'' OR a.attidentity<>'' OR f IN ('gregale_row_identity','gregale_row_version')) AND
   e.enrollment_id<>'00000000-0000-0000-0000-000000000000'::uuid AND e.fingerprint ~ '^[0-9a-f]{64}$' AND
   EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=c.oid AND attname='gregale_row_identity' AND atttypid='uuid'::regtype AND attnotnull AND NOT attisdropped) AND
   EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=c.oid AND attname='gregale_row_version' AND atttypid='int8'::regtype AND attnotnull AND NOT attisdropped),false) AS valid
 FROM gregale_repair.enrollments e LEFT JOIN pg_class c ON c.oid=e.relation LEFT JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE e.qualified_name=$1 LIMIT 2
), actual AS MATERIALIZED (
 SELECT e.*,(__FINGERPRINT_SQL__) AS live_fingerprint FROM entry e WHERE e.valid AND (SELECT count(*) FROM entry)=1
), trigger_issues AS (
 SELECT x.name FROM actual a CROSS JOIN (VALUES ('gregale_repair_guard','guard_row',31),('gregale_repair_capture','capture_row',17)) x(name,func,kind)
 LEFT JOIN pg_trigger t ON t.tgrelid=a.live_oid AND t.tgname=x.name
 LEFT JOIN pg_namespace n ON n.nspname='gregale_repair' LEFT JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname=x.func AND p.pronargs=0
 WHERE NOT coalesce(t.tgtype=x.kind AND t.tgenabled IN ('O','A') AND t.tgnargs=0 AND t.tgattr=''::int2vector AND t.tgqual IS NULL AND
  t.tgconstraint=0 AND t.tgfoid=p.oid AND t.tgoldtable IS NULL AND t.tgnewtable IS NULL AND NOT t.tgisinternal AND current_setting('session_replication_role')='origin',false)
), missing_grants AS (
 SELECT DISTINCT x.object FROM actual a CROSS JOIN LATERAL (VALUES
  ('table schema: USAGE',has_schema_privilege(a.relnamespace,'USAGE')),
  ('table: SELECT',has_table_privilege(a.live_oid,'SELECT')),
  ('table: UPDATE, DELETE or TRUNCATE (enrollment lock)',has_table_privilege(a.live_oid,'UPDATE,DELETE,TRUNCATE')),
  ('declared fields: UPDATE',(SELECT bool_and(has_column_privilege(a.live_oid,f,'UPDATE')) FROM unnest(a.fields) f))
 ) x(object,ok) WHERE NOT coalesce(x.ok,false)
), facts AS (
 SELECT (SELECT count(*) FROM entry) AS entries,(SELECT count(*)=1 FROM actual) AS valid,
  coalesce((SELECT live_fingerprint=fingerprint FROM actual),false) AS fingerprint_ok,
  ARRAY(SELECT name FROM trigger_issues ORDER BY name) AS bad_triggers,
  ARRAY(SELECT object FROM missing_grants ORDER BY object) AS missing_grants
)
SELECT jsonb_build_object('inspect_enrollment',false,'findings',
 CASE WHEN f.entries=0 THEN jsonb_build_array(jsonb_build_object('id','enrollment','code','not_enrolled','objects','[]'::jsonb))
 WHEN NOT f.valid THEN jsonb_build_array(jsonb_build_object('id','enrollment','code','enrollment_invalid','objects','[]'::jsonb))
 ELSE jsonb_build_array(
  jsonb_build_object('id','enrollment','code','enrolled','objects','[]'::jsonb),
  jsonb_build_object('id','fingerprint','code',CASE WHEN f.fingerprint_ok THEN 'fingerprint_matches' ELSE 'fingerprint_changed' END,'objects','[]'::jsonb),
  jsonb_build_object('id','triggers','code',CASE WHEN cardinality(f.bad_triggers)=0 THEN 'triggers_enabled' ELSE 'triggers_invalid' END,'objects',to_jsonb(f.bad_triggers))
 ) || CASE WHEN cardinality(f.missing_grants)=0 THEN '[]'::jsonb ELSE jsonb_build_array(jsonb_build_object('id','permissions','code','writer_permissions_missing','objects',to_jsonb(f.missing_grants))) END END
) AS probe FROM facts f
