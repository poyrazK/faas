-- Interpret frozen values using their enrolled PostgreSQL types before
-- comparing JSON representations. In particular, timestamptz output depends
-- on the observing session's timezone. Never rewrite historical evidence.
CREATE OR REPLACE FUNCTION gregale_repair.normalize_values(target regclass, recorded jsonb) RETURNS jsonb
LANGUAGE plpgsql STABLE SET search_path = pg_catalog SET extra_float_digits = '3' SET IntervalStyle = 'postgres' AS $$
DECLARE definitions text; normalized jsonb;
BEGIN
 SELECT string_agg(format('%I %s',a.attname,format_type(a.atttypid,a.atttypmod)),',' ORDER BY a.attnum)
  INTO definitions FROM pg_attribute a
  WHERE a.attrelid=target AND a.attnum>0 AND NOT a.attisdropped AND recorded ? a.attname;
 IF definitions IS NULL THEN RAISE EXCEPTION 'repair_invalid_fields' USING ERRCODE='22023'; END IF;
 -- Convert only recorded fields: unrelated columns, domains and their default
 -- expressions must not be evaluated as a side effect of observation.
 EXECUTE format('SELECT to_jsonb(r.*) FROM jsonb_to_record($1) AS r(%s)',definitions) INTO normalized USING recorded;
 RETURN normalized;
END $$;

-- Compare native output, including float zero signs and numeric scale. Use the
-- current row as the base so unrelated domains and raw JSON stay untouched.
CREATE OR REPLACE FUNCTION gregale_repair.values_match(observed anyelement, recorded jsonb) RETURNS boolean
LANGUAGE plpgsql STABLE SET search_path = pg_catalog SET extra_float_digits = '3' SET IntervalStyle = 'postgres' AS $$
DECLARE fields text; restored_fields text; matches boolean;
BEGIN
 IF jsonb_typeof(recorded) IS DISTINCT FROM 'object' OR recorded='{}'::jsonb THEN
  RAISE EXCEPTION 'repair_invalid_fields' USING ERRCODE='22023'; END IF;
 SELECT string_agg(format('($1).%I',k),',' ORDER BY k),string_agg(format('restored.%I',k),',' ORDER BY k)
 INTO fields,restored_fields FROM jsonb_object_keys(recorded) k;
 EXECUTE format('SELECT ROW(%s)::text IS NOT DISTINCT FROM ROW(%s)::text FROM jsonb_populate_record($1,$2) AS restored',fields,restored_fields)
  INTO matches USING observed,recorded;
 RETURN matches;
END $$;
