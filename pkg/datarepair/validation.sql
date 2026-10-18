-- Advisory reads only. Never refresh a document, approval, or receipt.
CREATE OR REPLACE FUNCTION gregale_repair.validate_plan(plan uuid, expected_scope jsonb, expected_digest text) RETURNS jsonb
LANGUAGE plpgsql STABLE SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans; e gregale_repair.enrollments; item jsonb; current_version text; values_match boolean; fields text;
 issues jsonb:='[]'; rows jsonb:='[]'; checked integer:=0; unchanged integer:=0; reason text;
 schema_changed boolean:=false;
BEGIN
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope;
 IF p.digest IS DISTINCT FROM expected_digest OR coalesce(p.retained_digest,p.digest) IS DISTINCT FROM
  encode(sha256(convert_to(p.document::text,'UTF8')),'hex') THEN
  RAISE EXCEPTION 'repair_plan_digest_mismatch' USING ERRCODE='40001'; END IF;
 IF p.expires_at<=transaction_timestamp() THEN issues:=issues||'"plan_expired"'::jsonb; END IF;
 IF p.applied_at IS NOT NULL THEN issues:=issues||'"plan_applied"'::jsonb; END IF;
 IF p.replacement_plan_id IS NOT NULL THEN issues:=issues||'"plan_replaced"'::jsonb; END IF;
 IF p.apply_resolution IS NOT NULL THEN issues:=issues||'"apply_closed"'::jsonb; END IF;
 IF p.payload_purged_at IS NOT NULL THEN issues:=issues||'"payload_purged"'::jsonb; END IF;
 IF (p.document->>'candidates')::int=0 THEN issues:=issues||'"no_candidates"'::jsonb; END IF;
 IF p.applied_at IS NULL AND p.payload_purged_at IS NULL AND p.replacement_plan_id IS NULL AND p.apply_resolution IS NULL THEN
  SELECT * INTO e FROM gregale_repair.enrollments WHERE enrollment_id=(p.document->>'enrollment_id')::uuid;
  schema_changed:=NOT FOUND;
  BEGIN
   IF NOT schema_changed THEN
    schema_changed:=e.fingerprint IS DISTINCT FROM p.document->>'schema_fingerprint' OR
     e.fingerprint IS DISTINCT FROM gregale_repair.fingerprint(e.relation) OR
     NOT EXISTS(SELECT 1 FROM pg_class WHERE oid=e.relation AND NOT relrowsecurity AND NOT relforcerowsecurity);
   END IF;
   IF NOT schema_changed THEN
    FOR item IN SELECT i FROM jsonb_array_elements(p.document->'items') i
     WHERE i->>'status'='candidate' ORDER BY i->>'row_identity' LOOP
     EXECUTE format('SELECT gregale_row_version::text FROM %s WHERE gregale_row_identity=$1',e.relation)
      INTO current_version USING (item->>'row_identity')::uuid;
     reason:=CASE WHEN current_version IS NULL THEN 'record_missing'
      WHEN current_version IS DISTINCT FROM item->>'expected_version' THEN 'record_version_changed' ELSE NULL END;
     IF reason IS NULL THEN
      SELECT string_agg(format('%L,t.%I',k,k),',' ORDER BY k) INTO fields FROM jsonb_object_keys(item->'after') k;
      EXECUTE format('SELECT jsonb_build_object(%s) @> $2 FROM %s t WHERE gregale_row_identity=$1',fields,e.relation)
       INTO values_match USING (item->>'row_identity')::uuid,item->'after';
      IF values_match IS DISTINCT FROM true THEN reason:='recorded_values_changed'; END IF;
     END IF;
     checked:=checked+1;
     IF reason IS NULL THEN unchanged:=unchanged+1;
     ELSE rows:=rows||jsonb_build_array(jsonb_build_object('row_identity',item->>'row_identity',
      'expected_version',item->>'expected_version','observed_version',current_version,'reason',reason));
     END IF;
    END LOOP;
   END IF;
  EXCEPTION WHEN undefined_table OR undefined_column OR undefined_object OR invalid_schema_name OR datatype_mismatch THEN
   schema_changed:=true; rows:='[]'; checked:=0; unchanged:=0;
  END;
  IF schema_changed THEN issues:=issues||'"schema_changed"'::jsonb; END IF;
 END IF;
 IF jsonb_array_length(rows)>0 THEN issues:=issues||'"candidate_drift"'::jsonb; END IF;
 RETURN jsonb_build_object('plan_id',p.id,'plan_digest',p.digest,'scope',p.scope,'checked_at',transaction_timestamp(),
  'expires_at',p.expires_at,'matches',issues='[]'::jsonb,'candidates',(p.document->>'candidates')::int,
  'excluded',(p.document->>'conflicts')::int+(p.document->>'unavailable')::int,
  'checked',checked,'unchanged',unchanged,'changed',jsonb_array_length(rows),'issues',issues,'rows',rows);
END $$;
