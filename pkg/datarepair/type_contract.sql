-- Logical signatures survive pg_dump/restore; local tuple identities fence DDL
-- within a capture transaction. Never certify older operations retroactively.
ALTER TABLE gregale_repair.enrollments ADD COLUMN IF NOT EXISTS type_fingerprint text CHECK(type_fingerprint ~ '^[0-9a-f]{64}$');
ALTER TABLE gregale_repair.operations ADD COLUMN IF NOT EXISTS type_fingerprint text CHECK(type_fingerprint ~ '^[0-9a-f]{64}$');
ALTER TABLE gregale_repair.operations ADD COLUMN IF NOT EXISTS capture_type_state text CHECK(capture_type_state ~ '^[0-9a-f]{64}$');

CREATE OR REPLACE FUNCTION gregale_repair.type_fingerprint(target regclass, physical boolean) RETURNS text
LANGUAGE sql STABLE SET search_path = pg_catalog SET extra_float_digits = '3' SET TimeZone = 'UTC' SET DateStyle = 'ISO, YMD' SET IntervalStyle = 'postgres' SET lc_monetary = 'C' SET jit = 'off' AS $$
WITH RECURSIVE settings AS MATERIALIZED (
 SELECT set_config('search_path','pg_catalog',true),set_config('extra_float_digits','3',true),set_config('TimeZone','UTC',true),
 set_config('DateStyle','ISO, YMD',true),set_config('IntervalStyle','postgres',true),set_config('lc_monetary','C',true)
), types(oid) AS (
 SELECT type_attribute.atttypid FROM pg_attribute type_attribute CROSS JOIN settings WHERE type_attribute.attrelid=target AND type_attribute.attnum>0 AND NOT type_attribute.attisdropped
 UNION
 SELECT nested.oid FROM types type_visited JOIN pg_type type_entry ON type_entry.oid=type_visited.oid CROSS JOIN LATERAL (
  SELECT type_entry.typbasetype AS oid WHERE type_entry.typtype='d'
  UNION SELECT type_entry.typelem WHERE type_entry.typelem<>0
  UNION SELECT type_attribute.atttypid FROM pg_attribute type_attribute WHERE type_attribute.attrelid=type_entry.typrelid AND type_attribute.attnum>0 AND NOT type_attribute.attisdropped
  UNION SELECT type_range.rngsubtype FROM pg_range type_range WHERE type_range.rngtypid=type_entry.oid OR type_range.rngmultitypid=type_entry.oid
  UNION SELECT type_range.rngtypid FROM pg_range type_range WHERE type_range.rngmultitypid=type_entry.oid
 ) nested WHERE nested.oid<>0
), definitions AS (
 SELECT format('%I.%I',type_namespace.nspname,type_entry.typname) AS name,jsonb_build_array(
  format('%I.%I',type_namespace.nspname,type_entry.typname),type_entry.typtype,type_entry.typcategory,type_entry.typlen,type_entry.typbyval,type_entry.typalign,type_entry.typstorage,type_entry.typnotnull,
  format_type(nullif(type_entry.typbasetype,0),type_entry.typtypmod),format_type(nullif(type_entry.typelem,0),NULL),type_entry.typdelim,
  pg_get_expr(type_entry.typdefaultbin,0),type_entry.typcollation::regcollation::text,
  (SELECT jsonb_agg(jsonb_build_array(type_constraint.conname,pg_get_constraintdef(type_constraint.oid),type_constraint.convalidated) ORDER BY type_constraint.conname) FROM pg_constraint type_constraint WHERE type_constraint.contypid=type_entry.oid),
  (SELECT jsonb_agg(type_enum.enumlabel ORDER BY type_enum.enumsortorder) FROM pg_enum type_enum WHERE type_enum.enumtypid=type_entry.oid),
  (SELECT jsonb_agg(jsonb_build_array(type_attribute.attname,format_type(type_attribute.atttypid,type_attribute.atttypmod),type_attribute.attnotnull,type_attribute.attcollation::regcollation::text) ORDER BY type_attribute.attnum)
   FROM pg_attribute type_attribute WHERE type_attribute.attrelid=type_entry.typrelid AND type_attribute.attnum>0 AND NOT type_attribute.attisdropped),
  (SELECT jsonb_agg(jsonb_build_array(format_type(type_range.rngsubtype,NULL),type_range.rngcollation::regcollation::text,
    (SELECT format('%I.%I',class_namespace.nspname,range_class.opcname) FROM pg_opclass range_class JOIN pg_namespace class_namespace ON class_namespace.oid=range_class.opcnamespace WHERE range_class.oid=type_range.rngsubopc),type_range.rngcanonical::regprocedure::text,type_range.rngsubdiff::regprocedure::text))
   FROM pg_range type_range WHERE type_range.rngtypid=type_entry.oid OR type_range.rngmultitypid=type_entry.oid),
  (SELECT jsonb_agg(jsonb_build_array(type_proc.oid::regprocedure::text,type_language.lanname,pg_get_functiondef(type_proc.oid),type_proc.prosrc,type_proc.probin,type_proc.provolatile,type_proc.proisstrict,type_proc.prosecdef,type_proc.proconfig) ORDER BY type_proc.oid::regprocedure::text)
   FROM pg_proc type_proc JOIN pg_language type_language ON type_language.oid=type_proc.prolang WHERE type_proc.oid IN(type_entry.typinput,type_entry.typoutput,type_entry.typreceive,type_entry.typsend,type_entry.typmodin,type_entry.typmodout,type_entry.typanalyze,type_entry.typsubscript) OR type_proc.oid IN(SELECT range_callback.rngcanonical FROM pg_range range_callback WHERE range_callback.rngtypid=type_entry.oid OR range_callback.rngmultitypid=type_entry.oid UNION SELECT range_callback.rngsubdiff FROM pg_range range_callback WHERE range_callback.rngtypid=type_entry.oid OR range_callback.rngmultitypid=type_entry.oid)),
  CASE WHEN physical THEN jsonb_build_array(type_entry.oid,type_entry.xmin::text,type_entry.ctid::text,
   (SELECT jsonb_agg(jsonb_build_array(type_attribute.attnum,type_attribute.xmin::text,type_attribute.ctid::text) ORDER BY type_attribute.attnum) FROM pg_attribute type_attribute WHERE type_attribute.attrelid=type_entry.typrelid),
   (SELECT jsonb_agg(jsonb_build_array(type_constraint.oid,type_constraint.xmin::text,type_constraint.ctid::text) ORDER BY type_constraint.oid) FROM pg_constraint type_constraint WHERE type_constraint.contypid=type_entry.oid),
   (SELECT jsonb_agg(jsonb_build_array(type_enum.oid,type_enum.xmin::text,type_enum.ctid::text) ORDER BY type_enum.oid) FROM pg_enum type_enum WHERE type_enum.enumtypid=type_entry.oid),
   (SELECT jsonb_agg(jsonb_build_array(type_proc.oid,type_proc.xmin::text,type_proc.ctid::text) ORDER BY type_proc.oid) FROM pg_proc type_proc WHERE type_proc.oid IN(type_entry.typinput,type_entry.typoutput,type_entry.typreceive,type_entry.typsend,type_entry.typmodin,type_entry.typmodout,type_entry.typanalyze,type_entry.typsubscript) OR type_proc.oid IN(SELECT range_callback.rngcanonical FROM pg_range range_callback WHERE range_callback.rngtypid=type_entry.oid OR range_callback.rngmultitypid=type_entry.oid UNION SELECT range_callback.rngsubdiff FROM pg_range range_callback WHERE range_callback.rngtypid=type_entry.oid OR range_callback.rngmultitypid=type_entry.oid))) ELSE NULL END
 ) AS definition FROM types type_visited JOIN pg_type type_entry ON type_entry.oid=type_visited.oid JOIN pg_namespace type_namespace ON type_namespace.oid=type_entry.typnamespace
 WHERE type_namespace.nspname<>'pg_catalog' OR type_entry.oid>=16384
)
SELECT encode(sha256(convert_to(coalesce(jsonb_agg(definition ORDER BY name),'[]'::jsonb)::text,'UTF8')),'hex') FROM definitions
$$;

-- This explicit owner action establishes the baseline for future captures only.
UPDATE gregale_repair.enrollments SET type_fingerprint=gregale_repair.type_fingerprint(relation,false);
