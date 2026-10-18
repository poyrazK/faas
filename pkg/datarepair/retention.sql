-- Installed atomically by the application database owner, never on startup.
ALTER TABLE gregale_repair.operations ADD COLUMN IF NOT EXISTS payload_purged_at timestamptz;
ALTER TABLE gregale_repair.plans ADD COLUMN IF NOT EXISTS payload_purged_at timestamptz;
ALTER TABLE gregale_repair.plans ADD COLUMN IF NOT EXISTS retained_digest text
 CHECK (retained_digest IS NULL OR retained_digest ~ '^[0-9a-f]{64}$');
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='gregale_repair.plans'::regclass AND conname='gregale_repair_plan_payload_marker') THEN
  ALTER TABLE gregale_repair.plans ADD CONSTRAINT gregale_repair_plan_payload_marker CHECK ((payload_purged_at IS NULL)=(retained_digest IS NULL));
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS gregale_repair.retention_runs (
 id uuid PRIMARY KEY CHECK (id<>'00000000-0000-0000-0000-000000000000'),
 scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
 document jsonb NOT NULL CHECK (jsonb_typeof(document)='object'),
 digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,
 applied_at timestamptz,
 applied_by text CHECK (octet_length(applied_by) BETWEEN 1 AND __ACTOR_MAX_BYTES__),
 applied_role text CHECK (octet_length(applied_role) BETWEEN 1 AND __ACTOR_MAX_BYTES__),
 CHECK ((applied_at IS NULL)=(applied_by IS NULL) AND (applied_at IS NULL)=(applied_role IS NULL))
);
CREATE INDEX IF NOT EXISTS gregale_repair_operations_retention
 ON gregale_repair.operations(scope,created_at,id) WHERE payload_purged_at IS NULL AND finalized;
CREATE INDEX IF NOT EXISTS gregale_repair_plans_retention
 ON gregale_repair.plans(scope,created_at,id) WHERE payload_purged_at IS NULL;
CREATE INDEX IF NOT EXISTS gregale_repair_plans_source ON gregale_repair.plans(source_operation_id);

-- Metadata-only classification keeps document hashing bounded to the selected
-- batch. A replacement is a source fence, but its saved child must also pass
-- the integrity/lineage checks in retention_item before any payload is removed.
CREATE OR REPLACE FUNCTION gregale_repair.retention_plan_reason(p gregale_repair.plans, cutoff timestamptz) RETURNS text
LANGUAGE plpgsql STABLE SET search_path = pg_catalog AS $$
DECLARE child_created_at timestamptz;
BEGIN
 IF p.replacement_plan_id IS NOT NULL THEN
  IF p.applied_at IS NOT NULL THEN RETURN 'replacement_unverified'; END IF;
  SELECT created_at INTO child_created_at FROM gregale_repair.plans
   WHERE id=p.replacement_plan_id AND scope=p.scope AND source_operation_id=p.source_operation_id AND id<>p.id;
  IF NOT FOUND THEN RETURN 'replacement_unverified'; END IF;
  IF p.created_at>cutoff OR child_created_at>cutoff THEN RETURN 'replacement_retention_minimum'; END IF;
  RETURN 'superseded_plan';
 ELSIF p.applied_at IS NOT NULL THEN
  IF p.created_at>cutoff OR p.applied_at>cutoff THEN RETURN 'receipt_retention_minimum'; END IF;
  RETURN 'applied_receipt_retained';
 ELSIF p.apply_resolution IS NOT NULL THEN
  -- Closure without a saved replacement still needs the old field values.
  RETURN 'closed_without_replacement';
 ELSIF p.approved_at IS NOT NULL THEN
  -- Expiry and elapsed time are not evidence about a lost COMMIT.
  RETURN 'unresolved_apply';
 ELSIF p.created_at>cutoff OR p.expires_at>cutoff THEN
  RETURN 'unapproved_plan_not_expired';
 END IF;
 RETURN 'expired_unapproved';
END $$;

-- Only metadata leaves these functions. Journals remain pinned by every
-- uncompacted plan, including a saved replacement of a compacted parent.
CREATE OR REPLACE FUNCTION gregale_repair.retention_item(kind text, item_id uuid, expected_scope jsonb, cutoff timestamptz) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans; child gregale_repair.plans; o gregale_repair.operations;
 payload_bytes bigint; rows_count bigint; reason text; proof jsonb:='{}';
 previous_candidates integer; previous_excluded integer; retained_candidates integer; removed_candidates integer; excluded integer;
BEGIN
 IF kind='plan' THEN
  SELECT * INTO p FROM gregale_repair.plans WHERE id=item_id AND scope=expected_scope AND payload_purged_at IS NULL;
  IF NOT FOUND THEN RETURN NULL; END IF;
  reason:=gregale_repair.retention_plan_reason(p,cutoff);
  IF reason NOT IN ('superseded_plan','applied_receipt_retained','expired_unapproved') THEN RETURN NULL; END IF;
  IF p.digest IS DISTINCT FROM encode(sha256(convert_to(p.document::text,'UTF8')),'hex') THEN
   RAISE EXCEPTION 'repair_plan_digest_mismatch' USING ERRCODE='40001';
  END IF;
  IF reason='superseded_plan' THEN
   SELECT * INTO child FROM gregale_repair.plans WHERE id=p.replacement_plan_id AND scope=expected_scope AND source_operation_id=p.source_operation_id;
   IF NOT FOUND OR coalesce(child.retained_digest,child.digest) IS DISTINCT FROM encode(sha256(convert_to(child.document::text,'UTF8')),'hex') OR
    child.document->'scope' IS DISTINCT FROM p.scope OR p.document->'scope' IS DISTINCT FROM p.scope OR
    child.document->>'source_operation_id' IS DISTINCT FROM p.source_operation_id::text OR
    p.document->>'source_operation_id' IS DISTINCT FROM p.source_operation_id::text OR
    child.document->'enrollment_id' IS DISTINCT FROM p.document->'enrollment_id' OR
    child.document->'schema_fingerprint' IS DISTINCT FROM p.document->'schema_fingerprint' OR
    child.document->'table' IS DISTINCT FROM p.document->'table' THEN
    RAISE EXCEPTION 'repair_retention_replacement_unverified' USING ERRCODE='40001';
   END IF;
   previous_candidates:=(p.document->>'candidates')::integer;
   previous_excluded:=(p.document->>'conflicts')::integer+(p.document->>'unavailable')::integer;
   retained_candidates:=(child.document->>'candidates')::integer;
   removed_candidates:=previous_candidates-retained_candidates;
   excluded:=(child.document->>'conflicts')::integer+(child.document->>'unavailable')::integer;
   IF (previous_candidates BETWEEN 1 AND __MAX_ROWS__ AND previous_excluded BETWEEN 0 AND __MAX_ROWS__-previous_candidates AND
    retained_candidates BETWEEN 0 AND previous_candidates AND excluded=previous_excluded+removed_candidates AND
    (child.document->>'conflicts')::integer>=0 AND (child.document->>'unavailable')::integer>=0 AND
    child.document->'replacement'=jsonb_build_object('parent_plan_id',p.id,'parent_plan_digest',p.digest,
     'previous_candidates',previous_candidates,'previous_excluded',previous_excluded,
     'retained_candidates',retained_candidates,'removed_candidates',removed_candidates) AND
    jsonb_typeof(child.document->'items')='array' AND
    jsonb_array_length(child.document->'items')=CASE WHEN child.payload_purged_at IS NULL THEN retained_candidates+excluded ELSE 0 END) IS DISTINCT FROM true THEN
    RAISE EXCEPTION 'repair_retention_replacement_unverified' USING ERRCODE='40001';
   END IF;
   proof:=jsonb_build_object('replacement_plan_id',child.id,'replacement_plan_digest',child.digest,'superseded_at',child.created_at);
  END IF;
  RETURN proof||jsonb_build_object('kind',kind,'id',p.id,'source_operation_id',p.source_operation_id,
   'record_digest',encode(sha256(convert_to(to_jsonb(p)::text,'UTF8')),'hex'),
   'payload_bytes',greatest(octet_length((p.document->'items')::text)-2,0),
   'change_rows',jsonb_array_length(p.document->'items'),'reason',reason);
 ELSIF kind='operation' THEN
  SELECT * INTO o FROM gregale_repair.operations WHERE id=item_id AND scope=expected_scope AND finalized AND payload_purged_at IS NULL AND created_at<=cutoff;
  IF NOT FOUND OR EXISTS(SELECT 1 FROM gregale_repair.plans WHERE payload_purged_at IS NULL AND (source_operation_id=item_id OR repair_operation_id=item_id)) THEN RETURN NULL; END IF;
  SELECT count(*),coalesce(sum(octet_length(key::text)+octet_length(before_values::text)+octet_length(after_values::text)),0)
   INTO rows_count,payload_bytes FROM gregale_repair.changes WHERE operation_id=item_id;
  RETURN jsonb_build_object('kind',kind,'id',o.id,'record_digest',encode(sha256(convert_to(to_jsonb(o)::text,'UTF8')),'hex'),
   'payload_bytes',payload_bytes,'change_rows',rows_count,'reason','unreferenced_capture');
 END IF;
 RAISE EXCEPTION 'repair_invalid_retention_item' USING ERRCODE='22023';
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.get_retention_plan(plan uuid, expected_scope jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE r gregale_repair.retention_runs;
BEGIN
 SELECT * INTO STRICT r FROM gregale_repair.retention_runs WHERE id=plan AND scope=expected_scope;
 RETURN r.document||jsonb_build_object('id',r.id,'digest',r.digest,'applied_at',r.applied_at,'applied_by',r.applied_by,'applied_role',r.applied_role);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.prepare_retention(expected_scope jsonb, requested_before timestamptz, batch_size integer) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE cutoff timestamptz:=coalesce(requested_before,clock_timestamp()-make_interval(secs=>__RETENTION_MIN__));
 plan uuid:=gen_random_uuid(); item jsonb; entries jsonb:='[]'; total_bytes bigint:=0; selected_count integer:=0;
 now_at timestamptz:=clock_timestamp(); expiry timestamptz; doc jsonb; held bigint; holds jsonb;
BEGIN
 IF expected_scope IS NULL OR jsonb_typeof(expected_scope)<>'object' OR NOT(expected_scope ?& ARRAY['account_id','app_id','environment']) OR
  expected_scope-'account_id'-'app_id'-'environment'-'platform_tenant_id'<>'{}'::jsonb OR
  EXISTS(SELECT 1 FROM jsonb_each(expected_scope) kv WHERE jsonb_typeof(kv.value)<>'string' OR length(kv.value#>>'{}') NOT BETWEEN 1 AND 256) OR
  batch_size IS NULL OR batch_size NOT BETWEEN 1 AND __PURGE_MAX_ITEMS__ OR cutoff>now_at-make_interval(secs=>__RETENTION_MIN__) THEN
  RAISE EXCEPTION 'repair_invalid_retention_policy' USING ERRCODE='22023';
 END IF;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 -- Bound candidate metadata before reading/hashing any plan documents. Plan
 -- compaction precedes source-journal removal, which needs another batch.
 FOR item IN
  WITH plan_ids AS (
   SELECT p.id,p.created_at FROM gregale_repair.plans p WHERE p.scope=expected_scope AND p.payload_purged_at IS NULL AND p.created_at<=cutoff
    AND gregale_repair.retention_plan_reason(p,cutoff) IN ('superseded_plan','applied_receipt_retained','expired_unapproved') ORDER BY p.created_at,p.id LIMIT batch_size
  ), operation_ids AS (
   SELECT o.id,o.created_at FROM gregale_repair.operations o WHERE o.scope=expected_scope AND o.payload_purged_at IS NULL AND o.finalized AND o.created_at<=cutoff
    AND NOT EXISTS(SELECT 1 FROM gregale_repair.plans p WHERE p.payload_purged_at IS NULL AND (p.source_operation_id=o.id OR p.repair_operation_id=o.id))
    ORDER BY o.created_at,o.id LIMIT batch_size
  ), candidates AS (SELECT 'plan' AS kind,id,created_at FROM plan_ids UNION ALL SELECT 'operation',id,created_at FROM operation_ids)
  SELECT gregale_repair.retention_item(kind,id,expected_scope,cutoff) FROM candidates ORDER BY created_at,id,kind
 LOOP
  IF item IS NULL THEN CONTINUE; END IF;
  IF selected_count>=batch_size THEN EXIT; END IF;
  IF total_bytes+(item->>'payload_bytes')::bigint>__PURGE_MAX_BYTES__ THEN CONTINUE; END IF;
  total_bytes:=total_bytes+(item->>'payload_bytes')::bigint;
  selected_count:=selected_count+1;
  entries:=entries||jsonb_build_array(item);
 END LOOP;
 -- Fixed reason groups keep hold reporting bounded without exporting row data
 -- or hashing every held document. Invalid selected child evidence aborts the
 -- inventory above; it cannot be legitimized by a new retention digest.
 WITH statuses AS MATERIALIZED (
  SELECT approved_at,applied_at,gregale_repair.retention_plan_reason(p,cutoff) AS reason
   FROM gregale_repair.plans p WHERE p.scope=expected_scope AND p.payload_purged_at IS NULL
 ), plan_holds AS (
  SELECT reason,count(*) AS count,count(*) FILTER(WHERE approved_at IS NOT NULL AND applied_at IS NULL) AS approved
   FROM statuses WHERE reason NOT IN ('superseded_plan','applied_receipt_retained','expired_unapproved') GROUP BY reason
 ), groups AS (
  SELECT 'plan' AS kind,reason,count,approved FROM plan_holds
  UNION ALL
  SELECT 'operation','plan_payload_required',count(*),0 FROM gregale_repair.operations o
   WHERE o.scope=expected_scope AND o.payload_purged_at IS NULL AND o.finalized AND o.created_at<=cutoff AND
    EXISTS(SELECT 1 FROM gregale_repair.plans p WHERE p.payload_purged_at IS NULL AND (p.source_operation_id=o.id OR p.repair_operation_id=o.id))
   HAVING count(*)>0
 )
 SELECT coalesce(sum(approved),0),coalesce(jsonb_agg(jsonb_build_object('kind',kind,'reason',reason,'count',count) ORDER BY kind,reason),'[]'::jsonb)
  INTO held,holds FROM groups;
 expiry:=now_at+make_interval(secs=>__PURGE_PLAN_TTL__);
 doc:=jsonb_build_object('scope',expected_scope,'before',cutoff,'created_at',now_at,'expires_at',expiry,
  'entries',entries,'estimated_payload_bytes',total_bytes,'held_approved_plans',held,'holds',holds,'requested_by_role',session_user);
 IF octet_length(doc::text)>__PURGE_MAX_PLAN_BYTES__ THEN RAISE EXCEPTION 'repair_retention_limit' USING ERRCODE='54000'; END IF;
 INSERT INTO gregale_repair.retention_runs(id,scope,document,digest,expires_at)
 VALUES(plan,expected_scope,doc,encode(sha256(convert_to(doc::text,'UTF8')),'hex'),expiry);
 RETURN gregale_repair.get_retention_plan(plan,expected_scope);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.apply_retention(plan uuid, expected_scope jsonb, expected_digest text, actor text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE r gregale_repair.retention_runs; item jsonb; current_item jsonb; cutoff timestamptz; at timestamptz:=clock_timestamp(); retained jsonb;
BEGIN
 IF actor IS NULL OR octet_length(actor) NOT BETWEEN 1 AND __ACTOR_MAX_BYTES__ THEN RAISE EXCEPTION 'repair_invalid_retention_actor' USING ERRCODE='22023'; END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'repair_retention_requires_read_committed' USING ERRCODE='22023'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(expected_scope::text,161018));
 SELECT * INTO STRICT r FROM gregale_repair.retention_runs WHERE id=plan AND scope=expected_scope FOR UPDATE;
 IF r.digest IS DISTINCT FROM expected_digest OR r.digest IS DISTINCT FROM encode(sha256(convert_to(r.document::text,'UTF8')),'hex') THEN
  RAISE EXCEPTION 'repair_retention_digest_mismatch' USING ERRCODE='40001';
 END IF;
 IF r.applied_at IS NOT NULL THEN RETURN gregale_repair.get_retention_plan(plan,expected_scope); END IF;
 IF r.expires_at<=clock_timestamp() THEN RAISE EXCEPTION 'repair_retention_plan_expired' USING ERRCODE='40001'; END IF;
 cutoff:=(r.document->>'before')::timestamptz;
 IF cutoff>clock_timestamp()-make_interval(secs=>__RETENTION_MIN__) OR jsonb_array_length(r.document->'entries')>__PURGE_MAX_ITEMS__ OR
  (r.document->>'estimated_payload_bytes')::bigint>__PURGE_MAX_BYTES__ THEN RAISE EXCEPTION 'repair_invalid_retention_policy' USING ERRCODE='22023'; END IF;
 -- Validate the whole frozen set before changing anything. A newly created
 -- plan pins its source, and any changed entry rejects the entire batch.
 FOR item IN SELECT v FROM jsonb_array_elements(r.document->'entries') v LOOP
  current_item:=gregale_repair.retention_item(item->>'kind',(item->>'id')::uuid,expected_scope,cutoff);
  IF current_item IS DISTINCT FROM item THEN RAISE EXCEPTION 'repair_retention_selection_changed' USING ERRCODE='40001'; END IF;
 END LOOP;
 PERFORM set_config('gregale.repair_purge',pg_current_xact_id()::text,true);
 FOR item IN SELECT v FROM jsonb_array_elements(r.document->'entries') v LOOP
  IF item->>'kind'='plan' THEN
   SELECT document||jsonb_build_object('items','[]'::jsonb) INTO STRICT retained FROM gregale_repair.plans WHERE id=(item->>'id')::uuid;
   UPDATE gregale_repair.plans SET document=retained,payload_purged_at=at,retained_digest=encode(sha256(convert_to(retained::text,'UTF8')),'hex') WHERE id=(item->>'id')::uuid;
  ELSE
   UPDATE gregale_repair.operations SET payload_purged_at=at WHERE id=(item->>'id')::uuid;
   DELETE FROM gregale_repair.changes WHERE operation_id=(item->>'id')::uuid;
  END IF;
 END LOOP;
 PERFORM set_config('gregale.repair_purge','',true);
 UPDATE gregale_repair.retention_runs SET applied_at=at,applied_by=actor,applied_role=session_user WHERE id=plan;
 RETURN gregale_repair.get_retention_plan(plan,expected_scope);
END $$;
