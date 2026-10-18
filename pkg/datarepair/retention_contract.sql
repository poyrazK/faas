-- Shared fresh-install/owner-upgrade repair functions.
CREATE OR REPLACE FUNCTION gregale_repair.protect_operation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
 IF current_user=(SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname='gregale_repair') AND
  current_setting('gregale.repair_purge',true)=pg_current_xact_id()::text AND TG_OP='UPDATE' AND OLD.finalized AND
  OLD.payload_purged_at IS NULL AND NEW.payload_purged_at IS NOT NULL AND
  (to_jsonb(NEW)-'payload_purged_at')=(to_jsonb(OLD)-'payload_purged_at') THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' OR OLD.finalized OR OLD.transaction_id<>pg_current_xact_id()::text OR
  (to_jsonb(NEW)-'finalized') IS DISTINCT FROM (to_jsonb(OLD)-'finalized') OR NOT NEW.finalized THEN
  RAISE EXCEPTION 'repair_journal_is_immutable' USING ERRCODE='22023';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS gregale_repair_protect_operation ON gregale_repair.operations;
CREATE TRIGGER gregale_repair_protect_operation BEFORE UPDATE OR DELETE ON gregale_repair.operations
FOR EACH ROW EXECUTE FUNCTION gregale_repair.protect_operation();

CREATE OR REPLACE FUNCTION gregale_repair.protect_change() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF current_user=(SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname='gregale_repair') AND
   current_setting('gregale.repair_purge',true)=pg_current_xact_id()::text AND EXISTS(
    SELECT 1 FROM gregale_repair.operations WHERE id=OLD.operation_id AND payload_purged_at IS NOT NULL) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'repair_journal_is_immutable' USING ERRCODE='22023';
 END IF;
 IF nullif(current_setting('gregale.repair_operation',true),'') IS DISTINCT FROM NEW.operation_id::text OR NOT EXISTS(
  SELECT 1 FROM gregale_repair.operations WHERE id=NEW.operation_id AND NOT finalized AND transaction_id=pg_current_xact_id()::text) THEN
  RAISE EXCEPTION 'repair_journal_is_immutable' USING ERRCODE='22023';
 END IF;
 IF TG_OP='UPDATE' AND ((to_jsonb(NEW)-ARRAY['after_values','after_version']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['after_values','after_version']) OR NEW.after_version<=OLD.after_version) THEN
  RAISE EXCEPTION 'repair_journal_is_immutable' USING ERRCODE='22023';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS gregale_repair_protect_change ON gregale_repair.changes;
CREATE TRIGGER gregale_repair_protect_change BEFORE INSERT OR UPDATE OR DELETE ON gregale_repair.changes
FOR EACH ROW EXECUTE FUNCTION gregale_repair.protect_change();


CREATE OR REPLACE FUNCTION gregale_repair.begin_operation(operation uuid, operation_scope jsonb, release_version text, target regclass, source_operation uuid DEFAULT NULL) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE e gregale_repair.enrollments;
BEGIN
 IF nullif(current_setting('gregale.repair_operation',true),'') IS NOT NULL OR operation_scope IS NULL OR jsonb_typeof(operation_scope)<>'object' OR
  NOT(operation_scope ?& ARRAY['account_id','app_id','environment']) OR operation_scope-'account_id'-'app_id'-'environment'-'platform_tenant_id'<>'{}'::jsonb OR
  EXISTS(SELECT 1 FROM jsonb_each(operation_scope) kv WHERE jsonb_typeof(kv.value)<>'string' OR length(kv.value#>>'{}') NOT BETWEEN 1 AND 256) THEN
  RAISE EXCEPTION 'repair_invalid_scope_or_nested_capture' USING ERRCODE='22023';
 END IF;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(operation_scope::text,161018));
 SELECT * INTO e FROM gregale_repair.check_enrollment((SELECT enrollment_id FROM gregale_repair.enrollments WHERE relation=target));
 IF e.enrollment_id IS NULL THEN RAISE EXCEPTION 'repair_table_not_enrolled' USING ERRCODE='22023'; END IF;
 INSERT INTO gregale_repair.operations(id,scope,release,enrollment_id,schema_fingerprint,transaction_id,repair_of)
 VALUES(operation,operation_scope,release_version,e.enrollment_id,e.fingerprint,pg_current_xact_id()::text,source_operation);
 PERFORM set_config('gregale.repair_operation',operation::text,true);
END $$;


CREATE OR REPLACE FUNCTION gregale_repair.get_plan(plan uuid, expected_scope jsonb) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans;
BEGIN
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope;
 RETURN p.document||jsonb_build_object('id',p.id,'digest',p.digest,'expires_at',p.expires_at,'rehearsed_at',p.rehearsed_at,'rehearsal',p.rehearsal,'approved_at',p.approved_at,'approved_by',p.approved_by,'applied_at',p.applied_at,'repair_operation_id',p.repair_operation_id,'payload_purged_at',p.payload_purged_at,'check_failure',p.check_failure,'apply_checks',p.apply_checks,'replacement_plan_id',p.replacement_plan_id,'apply_resolution',p.apply_resolution);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.create_plan(operation uuid, expected_scope jsonb, checks_digest text, requested_plan uuid DEFAULT NULL) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE op gregale_repair.operations; e gregale_repair.enrollments; c gregale_repair.changes; current_row jsonb;
 items jsonb:='[]'; item jsonb; inverse jsonb; expected jsonb; observed jsonb; reason text; state text;
 observed_bytes bigint; observed_total bigint:=0; omitted boolean;
 plan uuid:=coalesce(requested_plan,gen_random_uuid()); doc jsonb; prior gregale_repair.plans;
BEGIN
 IF checks_digest IS NULL OR checks_digest !~ '^[0-9a-f]{64}$' THEN RAISE EXCEPTION 'repair_invalid_checks' USING ERRCODE='22023'; END IF;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 PERFORM pg_advisory_xact_lock(hashtextextended(plan::text,0));
 SELECT * INTO prior FROM gregale_repair.plans WHERE id=plan;
 IF FOUND THEN
  IF prior.scope IS DISTINCT FROM expected_scope OR prior.source_operation_id IS DISTINCT FROM operation OR prior.document->>'checks_sha256' IS DISTINCT FROM checks_digest THEN
   RAISE EXCEPTION 'repair_plan_identity_conflict' USING ERRCODE='40001';
  END IF;
  RETURN gregale_repair.get_plan(plan,expected_scope);
 END IF;
 SELECT * INTO STRICT op FROM gregale_repair.operations WHERE id=operation AND scope=expected_scope AND finalized FOR SHARE;
 IF op.payload_purged_at IS NOT NULL THEN RAISE EXCEPTION 'repair_payload_purged' USING ERRCODE='55000'; END IF;
 e:=gregale_repair.check_enrollment(op.enrollment_id);
 IF op.schema_fingerprint<>e.fingerprint THEN RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001'; END IF;
 FOR c IN SELECT * FROM gregale_repair.changes WHERE operation_id=operation ORDER BY row_identity LOOP
  SELECT jsonb_object_agg(k,v),jsonb_object_agg(k,c.after_values->k) INTO inverse,expected FROM jsonb_each(c.before_values) kv(k,v) WHERE v IS DISTINCT FROM c.after_values->k;
  IF inverse IS NULL THEN CONTINUE; END IF;
  EXECUTE format('SELECT to_jsonb(t) FROM %s t WHERE gregale_row_identity=$1',e.relation) INTO current_row USING c.row_identity;
  reason:=CASE WHEN current_row IS NULL THEN 'record_missing'
   WHEN (current_row->>'gregale_row_version')::bigint<>c.after_version THEN 'record_version_changed'
   WHEN NOT current_row @> expected THEN 'recorded_values_changed' ELSE 'matches_recorded_write' END;
  state:=CASE WHEN reason='record_missing' THEN 'unavailable' WHEN reason='matches_recorded_write' THEN 'candidate' ELSE 'conflict' END;
  -- Observations contain only fields changed by the recorded operation, not
  -- unrelated columns. They are immutable review context, never an apply patch.
  SELECT jsonb_object_agg(k,current_row->k) INTO observed FROM jsonb_object_keys(inverse) k WHERE current_row IS NOT NULL;
  observed_bytes:=coalesce(octet_length(observed::text),0);
  omitted:=observed_bytes>__OBSERVED_ROW_BYTES__ OR observed_total+observed_bytes>__OBSERVED_PLAN_BYTES__;
  IF omitted THEN observed:=NULL; ELSE observed_total:=observed_total+observed_bytes; END IF;
  item:=jsonb_build_object('row_identity',c.row_identity,'key',c.key,'expected_version',c.after_version::text,
   'before',inverse,'after',expected,'status',state,'reason',reason,
   'observed_version',current_row->>'gregale_row_version','observed_values',observed,'observation_omitted',omitted);
  items:=items||jsonb_build_array(item);
 END LOOP;
 doc:=jsonb_build_object('source_operation_id',operation,'source_release',op.release,'scope',op.scope,'enrollment_id',e.enrollment_id,'table',e.qualified_name,'schema_fingerprint',e.fingerprint,'checks_sha256',checks_digest,'items',items,
  'candidates',(SELECT count(*) FROM jsonb_array_elements(items) i WHERE i->>'status'='candidate'),'conflicts',(SELECT count(*) FROM jsonb_array_elements(items) i WHERE i->>'status'='conflict'),'unavailable',(SELECT count(*) FROM jsonb_array_elements(items) i WHERE i->>'status'='unavailable'));
 INSERT INTO gregale_repair.plans(id,source_operation_id,scope,document,digest,expires_at) VALUES(plan,operation,op.scope,doc,encode(sha256(convert_to(doc::text,'UTF8')),'hex'),clock_timestamp()+make_interval(secs=>__PLAN_TTL__));
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.record_rehearsal(plan uuid, expected_scope jsonb, expected_digest text, evidence jsonb) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 IF jsonb_typeof(evidence) IS DISTINCT FROM 'object' OR evidence->>'passed' IS DISTINCT FROM 'true' OR evidence->>'plan_digest' IS DISTINCT FROM expected_digest OR coalesce(evidence->>'snapshot_sha256','') !~ '^[0-9a-f]{64}$' THEN
  RAISE EXCEPTION 'repair_invalid_rehearsal' USING ERRCODE='22023';
 END IF;
 UPDATE gregale_repair.plans SET rehearsed_at=clock_timestamp(),rehearsal=evidence,check_failure=NULL WHERE id=plan AND scope=expected_scope AND digest=expected_digest AND document->>'checks_sha256'=evidence->>'checks_sha256' AND expires_at>clock_timestamp() AND approved_at IS NULL AND applied_at IS NULL AND payload_purged_at IS NULL AND replacement_plan_id IS NULL AND apply_resolution IS NULL
  AND (NOT(document ? 'checks') OR gregale_repair.valid_check_report(document->'checks',evidence->'check_results',true));
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_stale_plan' USING ERRCODE='40001'; END IF;
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.approve_plan(plan uuid, expected_scope jsonb, expected_digest text, actor text) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans;
BEGIN
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope FOR UPDATE;
 IF p.apply_resolution IS NOT NULL THEN RAISE EXCEPTION 'repair_apply_closed' USING ERRCODE='40001'; END IF;
 IF p.replacement_plan_id IS NOT NULL THEN RAISE EXCEPTION 'repair_plan_replaced' USING ERRCODE='40001'; END IF;
 IF p.payload_purged_at IS NOT NULL THEN RAISE EXCEPTION 'repair_payload_purged' USING ERRCODE='55000'; END IF;
 IF p.digest IS DISTINCT FROM expected_digest OR p.expires_at<=clock_timestamp() OR p.rehearsed_at IS NULL OR p.rehearsal->>'plan_digest' IS DISTINCT FROM expected_digest OR (p.document->>'candidates')::int<1 OR actor IS NULL OR octet_length(actor) NOT BETWEEN 1 AND __ACTOR_MAX_BYTES__ THEN
  RAISE EXCEPTION 'repair_approval_requires_current_rehearsal' USING ERRCODE='40001';
 END IF;
 IF p.approved_at IS NOT NULL AND p.approved_by<>actor THEN RAISE EXCEPTION 'repair_approval_already_decided' USING ERRCODE='40001'; END IF;
 UPDATE gregale_repair.plans SET approved_at=coalesce(approved_at,clock_timestamp()),approved_by=actor WHERE id=plan;
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.apply_plan(plan uuid, expected_scope jsonb, expected_digest text, repair_operation uuid, repair_release text) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans; e gregale_repair.enrollments; item jsonb; current_row jsonb; assignments text; affected bigint; total int:=0;
BEGIN
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope FOR UPDATE;
 IF p.digest IS DISTINCT FROM expected_digest OR coalesce(p.retained_digest,p.digest) IS DISTINCT FROM encode(sha256(convert_to(p.document::text,'UTF8')),'hex') THEN RAISE EXCEPTION 'repair_plan_digest_mismatch' USING ERRCODE='40001'; END IF;
 IF p.applied_at IS NOT NULL THEN
  IF p.repair_operation_id<>repair_operation THEN RAISE EXCEPTION 'repair_already_applied' USING ERRCODE='40001'; END IF;
  RETURN gregale_repair.get_plan(plan,expected_scope);
 END IF;
 IF p.apply_resolution IS NOT NULL THEN RAISE EXCEPTION 'repair_apply_closed' USING ERRCODE='40001'; END IF;
 IF p.replacement_plan_id IS NOT NULL THEN RAISE EXCEPTION 'repair_plan_replaced' USING ERRCODE='40001'; END IF;
 IF p.payload_purged_at IS NOT NULL THEN RAISE EXCEPTION 'repair_payload_purged' USING ERRCODE='55000'; END IF;
 IF p.approved_at IS NULL OR p.rehearsed_at IS NULL OR p.expires_at<=clock_timestamp() OR (p.document->>'candidates')::int<1 THEN
  RAISE EXCEPTION 'repair_plan_not_approved_or_expired' USING ERRCODE='40001';
 END IF;
 e:=gregale_repair.check_enrollment((p.document->>'enrollment_id')::uuid);
 IF e.fingerprint<>p.document->>'schema_fingerprint' THEN RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001'; END IF;
 PERFORM gregale_repair.begin_operation(repair_operation,expected_scope,repair_release,e.relation,p.source_operation_id);
 FOR item IN SELECT i FROM jsonb_array_elements(p.document->'items') i WHERE i->>'status'='candidate' ORDER BY i->>'row_identity' LOOP
  EXECUTE format('SELECT to_jsonb(t) FROM %s t WHERE gregale_row_identity=$1 FOR UPDATE',e.relation) INTO current_row USING (item->>'row_identity')::uuid;
  IF current_row IS NULL OR current_row->>'gregale_row_version'<>item->>'expected_version' OR NOT current_row @> (item->'after') THEN
   RAISE EXCEPTION 'repair_row_conflict' USING ERRCODE='40001';
  END IF;
  SELECT string_agg(format('%I=(jsonb_populate_record(NULL::%s,$1)).%I',k,e.relation,k),',' ORDER BY k) INTO assignments FROM jsonb_object_keys(item->'before') k;
  EXECUTE format('UPDATE %s SET %s WHERE gregale_row_identity=$2 AND gregale_row_version=$3',e.relation,assignments) USING item->'before',(item->>'row_identity')::uuid,(item->>'expected_version')::bigint;
  GET DIAGNOSTICS affected=ROW_COUNT;
  IF affected<>1 THEN RAISE EXCEPTION 'repair_row_conflict' USING ERRCODE='40001'; END IF;
  total:=total+1;
 END LOOP;
 IF total<>(p.document->>'candidates')::int THEN RAISE EXCEPTION 'repair_plan_incomplete' USING ERRCODE='40001'; END IF;
 PERFORM gregale_repair.finish_operation(repair_operation);
 SET CONSTRAINTS ALL IMMEDIATE;
 UPDATE gregale_repair.plans SET applied_at=clock_timestamp(),repair_operation_id=repair_operation WHERE id=plan;
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;
