-- Explicit owner upgrade. Query text stays in operator configuration.
ALTER TABLE gregale_repair.plans ADD COLUMN IF NOT EXISTS check_failure jsonb
 CHECK (check_failure IS NULL OR jsonb_typeof(check_failure)='object');
ALTER TABLE gregale_repair.plans ADD COLUMN IF NOT EXISTS apply_checks jsonb
 CHECK (apply_checks IS NULL OR jsonb_typeof(apply_checks)='object');

CREATE OR REPLACE FUNCTION gregale_repair.valid_check_manifest(manifest jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog AS $$
DECLARE c jsonb; names text[]:='{}';
BEGIN
 IF jsonb_typeof(manifest) IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 IF jsonb_array_length(manifest) NOT BETWEEN 1 AND __MAX_CHECKS__ THEN RETURN false; END IF;
 FOR c IN SELECT v FROM jsonb_array_elements(manifest) v LOOP
  IF jsonb_typeof(c) IS DISTINCT FROM 'object' OR c-'name'-'sql_sha256'<>'{}'::jsonb OR
   coalesce(c->>'name','') !~ '^[A-Za-z][A-Za-z0-9 ._-]*$' OR c->>'name'<>btrim(c->>'name') OR
   octet_length(c->>'name')>__CHECK_NAME_BYTES__ OR coalesce(c->>'sql_sha256','') !~ '^[0-9a-f]{64}$' OR
   array_position(names,c->>'name') IS NOT NULL THEN RETURN false; END IF;
  names:=array_append(names,c->>'name');
 END LOOP;
 RETURN true;
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.valid_check_report(manifest jsonb, report jsonb, passed boolean) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog AS $$
DECLARE c jsonb; r jsonb; i integer:=0; failed boolean:=false;
BEGIN
 IF NOT gregale_repair.valid_check_manifest(manifest) OR jsonb_typeof(report) IS DISTINCT FROM 'object' OR
  report-'results'<>'{}'::jsonb OR jsonb_typeof(report->'results') IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 IF jsonb_array_length(report->'results')<>jsonb_array_length(manifest) THEN RETURN false; END IF;
 FOR c IN SELECT v FROM jsonb_array_elements(manifest) v LOOP
  r:=report->'results'->i; i:=i+1;
  IF jsonb_typeof(r) IS DISTINCT FROM 'object' OR r-'name'-'sql_sha256'-'status'<>'{}'::jsonb OR
   r->>'name' IS DISTINCT FROM c->>'name' OR r->>'sql_sha256' IS DISTINCT FROM c->>'sql_sha256' THEN RETURN false; END IF;
  IF failed THEN
   IF r->>'status' IS DISTINCT FROM 'not_run' THEN RETURN false; END IF;
  ELSIF r->>'status' IN ('failed','error') THEN failed:=true;
  ELSIF r->>'status' IS DISTINCT FROM 'passed' THEN RETURN false;
  END IF;
 END LOOP;
 RETURN passed IS NOT DISTINCT FROM NOT failed;
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.create_plan_with_checks(operation uuid, expected_scope jsonb,
 checks_digest text, requested_plan uuid, manifest jsonb) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE plan uuid:=coalesce(requested_plan,gen_random_uuid()); prior gregale_repair.plans; doc jsonb;
BEGIN
 IF gregale_repair.valid_check_manifest(manifest) IS DISTINCT FROM true THEN
  RAISE EXCEPTION 'repair_invalid_checks' USING ERRCODE='22023'; END IF;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 PERFORM pg_advisory_xact_lock(hashtextextended(plan::text,0));
 SELECT * INTO prior FROM gregale_repair.plans WHERE id=plan;
 IF FOUND THEN
  IF prior.scope IS DISTINCT FROM expected_scope OR prior.source_operation_id IS DISTINCT FROM operation OR
   prior.document->>'checks_sha256' IS DISTINCT FROM checks_digest OR prior.document->'checks' IS DISTINCT FROM manifest THEN
   RAISE EXCEPTION 'repair_plan_identity_conflict' USING ERRCODE='40001'; END IF;
  RETURN gregale_repair.get_plan(plan,expected_scope);
 END IF;
 -- The canonical constructor owns capture/schema classification. Bind the
 -- manifest before this transaction exposes the new immutable plan identity.
 PERFORM gregale_repair.create_plan(operation,expected_scope,checks_digest,plan);
 SELECT document||jsonb_build_object('checks',manifest) INTO STRICT doc FROM gregale_repair.plans WHERE id=plan;
 UPDATE gregale_repair.plans SET document=doc,digest=encode(sha256(convert_to(doc::text,'UTF8')),'hex') WHERE id=plan;
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.evaluate_checks(policy jsonb) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE c jsonb; manifest jsonb; results jsonb:='[]'; state text; failed boolean:=false;
 value_row json; value_type oid; columns_count bigint; query text;
BEGIN
 IF jsonb_typeof(policy) IS DISTINCT FROM 'object' OR policy-'version'-'checks'<>'{}'::jsonb OR
  policy->>'version' IS DISTINCT FROM '1' OR jsonb_typeof(policy->'checks') IS DISTINCT FROM 'array' THEN
  RAISE EXCEPTION 'repair_invalid_checks' USING ERRCODE='22023'; END IF;
 IF jsonb_array_length(policy->'checks') NOT BETWEEN 1 AND __MAX_CHECKS__ THEN
  RAISE EXCEPTION 'repair_invalid_checks' USING ERRCODE='22023'; END IF;
 SELECT jsonb_agg(jsonb_build_object('name',v->>'name','sql_sha256',encode(sha256(convert_to(v->>'sql','UTF8')),'hex')) ORDER BY ord)
 INTO manifest FROM jsonb_array_elements(policy->'checks') WITH ORDINALITY a(v,ord);
 IF gregale_repair.valid_check_manifest(manifest) IS DISTINCT FROM true OR EXISTS(
  SELECT 1 FROM jsonb_array_elements(policy->'checks') v WHERE jsonb_typeof(v) IS DISTINCT FROM 'object' OR
   v-'name'-'sql'<>'{}'::jsonb OR jsonb_typeof(v->'sql') IS DISTINCT FROM 'string' OR v->>'sql' !~* '^[[:space:]]*SELECT[[:space:]]') OR
  (SELECT sum(octet_length(v->>'sql')+octet_length(v->>'name')) FROM jsonb_array_elements(policy->'checks') v)>__CHECK_POLICY_BYTES__ THEN
  RAISE EXCEPTION 'repair_invalid_checks' USING ERRCODE='22023'; END IF;
 FOR c IN SELECT v FROM jsonb_array_elements(policy->'checks') v LOOP
  state:='not_run';
  IF NOT failed THEN
   BEGIN
    query:=regexp_replace(c->>'sql',';[[:space:]]*$','');
    EXECUTE format('SELECT row_to_json(q),pg_typeof(q.value)::oid FROM (%s%s) q(value)',query,E'\n') INTO STRICT value_row,value_type;
    SELECT count(*) INTO columns_count FROM json_object_keys(value_row);
    state:=CASE WHEN value_type<>16 OR columns_count<>1 THEN 'error'
     WHEN (value_row->'value')::jsonb='true'::jsonb THEN 'passed' ELSE 'failed' END;
   EXCEPTION WHEN OTHERS THEN state:='error';
   END;
   failed:=state<>'passed';
  END IF;
  results:=results||jsonb_build_array(jsonb_build_object('name',c->>'name',
   'sql_sha256',encode(sha256(convert_to(c->>'sql','UTF8')),'hex'),'status',state));
 END LOOP;
 RETURN jsonb_build_object('results',results);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.record_check_failure(plan uuid, expected_scope jsonb,
 expected_digest text, stage text, evidence jsonb) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans;
BEGIN
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope FOR UPDATE;
 IF p.digest IS DISTINCT FROM expected_digest OR p.applied_at IS NOT NULL OR p.payload_purged_at IS NOT NULL OR p.replacement_plan_id IS NOT NULL OR p.apply_resolution IS NOT NULL OR
  coalesce(stage,'') NOT IN ('rehearsal','apply') OR (stage='rehearsal' AND p.approved_at IS NOT NULL) OR
  evidence->>'passed' IS DISTINCT FROM 'false' OR evidence->>'plan_digest' IS DISTINCT FROM p.digest OR
  evidence->>'checks_sha256' IS DISTINCT FROM p.document->>'checks_sha256' OR
  gregale_repair.valid_check_report(p.document->'checks',evidence->'check_results',false) IS DISTINCT FROM true THEN
  RAISE EXCEPTION 'repair_invalid_check_failure' USING ERRCODE='40001'; END IF;
 UPDATE gregale_repair.plans SET check_failure=jsonb_build_object('stage',stage,'evidence',evidence,'recorded_at',clock_timestamp()),
  rehearsed_at=CASE WHEN stage='rehearsal' THEN NULL ELSE rehearsed_at END,
  rehearsal=CASE WHEN stage='rehearsal' THEN NULL ELSE rehearsal END WHERE id=plan;
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;
