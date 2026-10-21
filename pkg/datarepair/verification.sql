-- Observe a committed repair through its own journal, independently of the
-- pre-apply selection. This function never refreshes evidence or writes data.
CREATE OR REPLACE FUNCTION gregale_repair.verify_plan(plan uuid, expected_scope jsonb, expected_digest text) RETURNS jsonb
LANGUAGE plpgsql STABLE SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans; op gregale_repair.operations; e gregale_repair.enrollments; c gregale_repair.changes;
 current_version text; values_match boolean; fields text; reason text; key_text text; omitted boolean;
 issues jsonb:='[]'; rows jsonb:='[]'; checked integer:=0; unchanged integer:=0; edited integer:=0; missing integer:=0;
 schema_changed boolean:=false; journal_rows bigint; key_bytes integer:=0;
BEGIN
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope;
 IF p.digest IS DISTINCT FROM expected_digest OR coalesce(p.retained_digest,p.digest) IS DISTINCT FROM
  encode(sha256(convert_to(p.document::text,'UTF8')),'hex') THEN
  RAISE EXCEPTION 'repair_plan_digest_mismatch' USING ERRCODE='40001'; END IF;
 IF p.applied_at IS NULL OR p.repair_operation_id IS NULL THEN
  -- No receipt is not proof of rollback; resolution is a separate action.
  RAISE EXCEPTION 'repair_verification_requires_receipt' USING ERRCODE='40001'; END IF;
 SELECT * INTO op FROM gregale_repair.operations WHERE id=p.repair_operation_id AND scope=expected_scope AND finalized
  AND repair_of=p.source_operation_id AND enrollment_id=(p.document->>'enrollment_id')::uuid
  AND schema_fingerprint=p.document->>'schema_fingerprint';
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_verification_receipt_mismatch' USING ERRCODE='40001'; END IF;
 IF op.payload_purged_at IS NOT NULL THEN
  issues:=issues||'"journal_purged"'::jsonb;
 ELSE
  SELECT count(*) INTO journal_rows FROM gregale_repair.changes WHERE operation_id=op.id;
  IF journal_rows IS DISTINCT FROM (p.document->>'candidates')::integer OR journal_rows NOT BETWEEN 1 AND __MAX_ROWS__ THEN
   RAISE EXCEPTION 'repair_verification_journal_mismatch' USING ERRCODE='40001'; END IF;
  SELECT * INTO e FROM gregale_repair.enrollments WHERE enrollment_id=op.enrollment_id;
  schema_changed:=NOT FOUND;
  BEGIN
   IF NOT schema_changed THEN
    schema_changed:=e.fingerprint IS DISTINCT FROM op.schema_fingerprint OR
     e.fingerprint IS DISTINCT FROM gregale_repair.fingerprint(e.relation) OR
     NOT EXISTS(SELECT 1 FROM pg_class WHERE oid=e.relation AND NOT relrowsecurity AND NOT relforcerowsecurity);
   END IF;
   IF NOT schema_changed THEN
    FOR c IN SELECT * FROM gregale_repair.changes WHERE operation_id=op.id ORDER BY row_identity LOOP
     SELECT string_agg(format('%L,t.%I',k,k),',' ORDER BY k) INTO fields FROM jsonb_object_keys(c.after_values) k;
     EXECUTE format('SELECT gregale_row_version::text,jsonb_build_object(%s) @> $2 FROM %s t WHERE gregale_row_identity=$1',fields,e.relation)
      INTO current_version,values_match USING c.row_identity,c.after_values;
     reason:=CASE WHEN current_version IS NULL THEN 'record_missing'
      WHEN current_version IS DISTINCT FROM c.after_version::text THEN 'record_version_changed'
      WHEN values_match IS DISTINCT FROM true THEN 'recorded_values_changed' ELSE NULL END;
     checked:=checked+1;
     IF reason IS NULL THEN unchanged:=unchanged+1;
     ELSE
      IF reason='record_missing' THEN missing:=missing+1; ELSE edited:=edited+1; END IF;
      key_text:=c.key::text;
      omitted:=octet_length(key_text)>__OBSERVED_ROW_BYTES__ OR key_bytes+octet_length(key_text)>__OBSERVED_PLAN_BYTES__;
      IF omitted THEN key_text:=NULL; ELSE key_bytes:=key_bytes+octet_length(key_text); END IF;
      rows:=rows||jsonb_build_array(jsonb_build_object('row_identity',c.row_identity,'key_json',key_text,'key_omitted',omitted,
       'expected_version',c.after_version::text,'observed_version',current_version,'reason',reason));
     END IF;
    END LOOP;
   END IF;
  EXCEPTION WHEN undefined_table OR undefined_column OR undefined_object OR invalid_schema_name OR datatype_mismatch THEN
   schema_changed:=true;
  END;
  IF schema_changed THEN
   issues:=issues||'"schema_changed"'::jsonb; rows:='[]'; checked:=0; unchanged:=0; edited:=0; missing:=0;
  END IF;
 END IF;
 RETURN jsonb_build_object('plan_id',p.id,'plan_digest',p.digest,'scope',p.scope,'source_operation_id',p.source_operation_id,
  'repair_operation_id',p.repair_operation_id,'applied_at',p.applied_at,'checked_at',transaction_timestamp(),
  'restored',(p.document->>'candidates')::integer,'excluded',(p.document->>'conflicts')::integer+(p.document->>'unavailable')::integer,
  'available',issues='[]'::jsonb,'checked',checked,'unchanged',unchanged,'edited',edited,'missing',missing,'issues',issues,'rows',rows);
END $$;
