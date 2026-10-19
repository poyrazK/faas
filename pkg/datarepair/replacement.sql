-- The owner installs this contract explicitly. The parent's frozen document,
-- expiry and approval are never rewritten; this separate lifecycle pointer
-- fences every subsequent attempt to approve, publish evidence or apply it.
ALTER TABLE gregale_repair.plans ADD COLUMN IF NOT EXISTS replacement_plan_id uuid
 UNIQUE REFERENCES gregale_repair.plans(id) CHECK (replacement_plan_id<>id);

CREATE OR REPLACE FUNCTION gregale_repair.replace_plan(parent uuid, expected_scope jsonb,
 expected_digest text, requested_plan uuid, checks_digest text, manifest jsonb DEFAULT NULL) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans; prior gregale_repair.plans; e gregale_repair.enrollments;
 item jsonb; observed jsonb; current_row jsonb; reason text; state text; doc jsonb; fields text;
 items jsonb:='[]'; observed_bytes bigint; observed_total bigint:=0; omitted boolean;
 candidates integer; conflicts integer; unavailable integer;
BEGIN
 IF requested_plan IS NULL OR requested_plan='00000000-0000-0000-0000-000000000000'::uuid OR requested_plan=parent OR
  checks_digest IS NULL OR checks_digest !~ '^[0-9a-f]{64}$' OR
  (manifest IS NOT NULL AND gregale_repair.valid_check_manifest(manifest) IS DISTINCT FROM true) THEN
  RAISE EXCEPTION 'repair_invalid_replacement' USING ERRCODE='22023'; END IF;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 PERFORM pg_advisory_xact_lock(hashtextextended(requested_plan::text,0));
 -- Apply takes this same lock through COMMIT, including its business checks.
 -- A receipt observed here rejects replacement; otherwise the new fence and
 -- child plan commit atomically before another apply can acquire the lock.
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=parent AND scope=expected_scope FOR UPDATE;
 IF requested_plan=p.source_operation_id THEN RAISE EXCEPTION 'repair_invalid_replacement' USING ERRCODE='22023'; END IF;
 IF p.digest IS DISTINCT FROM expected_digest OR coalesce(p.retained_digest,p.digest) IS DISTINCT FROM
  encode(sha256(convert_to(p.document::text,'UTF8')),'hex') THEN
  RAISE EXCEPTION 'repair_plan_digest_mismatch' USING ERRCODE='40001'; END IF;
 SELECT * INTO prior FROM gregale_repair.plans WHERE id=requested_plan;
 IF FOUND THEN
  IF p.replacement_plan_id IS DISTINCT FROM requested_plan OR prior.scope IS DISTINCT FROM expected_scope OR
   prior.source_operation_id IS DISTINCT FROM p.source_operation_id OR coalesce(prior.retained_digest,prior.digest) IS DISTINCT FROM
    encode(sha256(convert_to(prior.document::text,'UTF8')),'hex') OR
   prior.document->'replacement'->>'parent_plan_id' IS DISTINCT FROM parent::text OR
   prior.document->'replacement'->>'parent_plan_digest' IS DISTINCT FROM expected_digest THEN
   RAISE EXCEPTION 'repair_replacement_identity_conflict' USING ERRCODE='40001'; END IF;
  RETURN gregale_repair.get_plan(requested_plan,expected_scope);
 END IF;
 IF p.applied_at IS NOT NULL OR p.replacement_plan_id IS NOT NULL OR (p.document->>'candidates')::integer<1 THEN
  RAISE EXCEPTION 'repair_plan_cannot_be_replaced' USING ERRCODE='40001'; END IF;
 IF p.payload_purged_at IS NOT NULL THEN RAISE EXCEPTION 'repair_payload_purged' USING ERRCODE='55000'; END IF;
 e:=gregale_repair.check_enrollment((p.document->>'enrollment_id')::uuid);
 IF e.fingerprint IS DISTINCT FROM p.document->>'schema_fingerprint' THEN
  RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001'; END IF;
 -- Excluded rows are copied, never queried or promoted. Reserve their frozen
 -- diagnostic budget before observing the original candidates again.
 SELECT coalesce(sum(octet_length((i->'observed_values')::text)) FILTER(WHERE i->'observed_values'<>'null'::jsonb),0)
  INTO observed_total FROM jsonb_array_elements(p.document->'items') i WHERE i->>'status'<>'candidate';
 FOR item IN SELECT i FROM jsonb_array_elements(p.document->'items') i ORDER BY i->>'row_identity' LOOP
  item:=item||jsonb_build_object('previous_status',item->>'status');
  IF item->>'status'='candidate' THEN
   SELECT string_agg(format('%L,t.%I',k,k),',' ORDER BY k) INTO fields FROM jsonb_object_keys(item->'after') k;
   EXECUTE format('SELECT jsonb_build_object(''gregale_row_version'',t.gregale_row_version::text,%s) FROM %s t WHERE gregale_row_identity=$1',fields,e.relation)
    INTO current_row USING (item->>'row_identity')::uuid;
   reason:=CASE WHEN current_row IS NULL THEN 'record_missing'
    WHEN current_row->>'gregale_row_version' IS DISTINCT FROM item->>'expected_version' THEN 'record_version_changed'
    WHEN NOT current_row @> (item->'after') THEN 'recorded_values_changed' ELSE 'matches_recorded_write' END;
   state:=CASE WHEN reason='record_missing' THEN 'unavailable' WHEN reason='matches_recorded_write' THEN 'candidate' ELSE 'conflict' END;
   SELECT jsonb_object_agg(k,current_row->k) INTO observed FROM jsonb_object_keys(item->'after') k WHERE current_row IS NOT NULL;
   observed_bytes:=coalesce(octet_length(observed::text),0);
   omitted:=observed_bytes>__OBSERVED_ROW_BYTES__ OR observed_total+observed_bytes>__OBSERVED_PLAN_BYTES__;
   IF omitted THEN observed:=NULL; ELSE observed_total:=observed_total+observed_bytes; END IF;
   item:=item||jsonb_build_object('status',state,'reason',reason,'observed_version',current_row->>'gregale_row_version',
    'observed_values',observed,'observation_omitted',omitted);
  END IF;
  items:=items||jsonb_build_array(item);
 END LOOP;
 SELECT count(*) FILTER(WHERE i->>'status'='candidate'),count(*) FILTER(WHERE i->>'status'='conflict'),
  count(*) FILTER(WHERE i->>'status'='unavailable') INTO candidates,conflicts,unavailable FROM jsonb_array_elements(items) i;
 doc:=(p.document-'checks'-'replacement')||jsonb_build_object('checks_sha256',checks_digest,'items',items,
  'candidates',candidates,'conflicts',conflicts,'unavailable',unavailable,'replacement',jsonb_build_object(
   'parent_plan_id',p.id,'parent_plan_digest',p.digest,'previous_candidates',(p.document->>'candidates')::integer,
   'retained_candidates',candidates,'removed_candidates',(p.document->>'candidates')::integer-candidates,
   'previous_excluded',(p.document->>'conflicts')::integer+(p.document->>'unavailable')::integer));
 IF manifest IS NOT NULL THEN doc:=doc||jsonb_build_object('checks',manifest); END IF;
 IF jsonb_array_length(items)>__MAX_ROWS__ OR octet_length(doc::text)>__MAX_PLAN_BYTES__ THEN
  RAISE EXCEPTION 'repair_replacement_limit' USING ERRCODE='54000'; END IF;
 INSERT INTO gregale_repair.plans(id,source_operation_id,scope,document,digest,expires_at)
 VALUES(requested_plan,p.source_operation_id,p.scope,doc,encode(sha256(convert_to(doc::text,'UTF8')),'hex'),
  clock_timestamp()+make_interval(secs=>__PLAN_TTL__));
 UPDATE gregale_repair.plans SET replacement_plan_id=requested_plan WHERE id=parent;
 RETURN gregale_repair.get_plan(requested_plan,expected_scope);
END $$;
