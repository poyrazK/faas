-- filename: 20261006111300001_application_standard_snapshot_resume_receipts.sql
-- adr: 595. Bind measured resume to immutable paused load and fresh publication.
-- Previously issued migration and protobuf bytes remain unchanged when proof is absent.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_restore_wire_fields(kind text) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE kind
 WHEN 'evidence' THEN '[[1,"version","u32"],[2,"capture_token","string"],[3,"fc_version","string"],[4,"capture","capture"]]'::jsonb
 WHEN 'capture' THEN '[[1,"version","u32"],[2,"parent","receipt"],[3,"memory","artifact"],[4,"vmstate","artifact"],[5,"private_drive","artifact"],[6,"captured_at_unix_nano","i64"]]'::jsonb
 WHEN 'artifact' THEN '[[1,"storage_key","string"],[2,"digest","string"],[3,"bytes","i64"]]'::jsonb
 WHEN 'source' THEN '[[1,"kind","string"],[2,"workload_name","string"],[3,"storage_key","string"],[4,"digest","string"],[5,"bytes","i64"]]'::jsonb
 WHEN 'drive' THEN '[[1,"source","source"],[2,"drive_id","string"],[3,"read_only","bool"],[4,"root_device","bool"],[5,"producer_digest","string"],[6,"producer_bytes","i64"],[7,"injected_digest","string"],[8,"injected_bytes","i64"]]'::jsonb
 WHEN 'consumption' THEN '[[1,"config_hash","string"],[2,"process_pid","u32"],[3,"process_start","string"],[4,"drives","drives"]]'::jsonb
 WHEN 'receipt' THEN '[[1,"binding","binding"],[2,"native_input_hash","string"],[3,"netns","string"],[4,"host_ip","string"],[5,"lease_uid","i32"],[6,"method","i32"],[7,"paused","bool"],[8,"completed_at_unix_nano","i64"],[9,"artifact_consumption","consumption"],[10,"snapshot_consumption","snapshot-consumption"],[11,"snapshot_resume_evidence","resume"]]'::jsonb
 WHEN 'snapshot-consumption' THEN '[[1,"version","u32"],[2,"capture_token","string"],[3,"evidence_hash","string"],[4,"memory","artifact"],[5,"vmstate","artifact"],[6,"private_drive","artifact"],[7,"mapped_memory_bytes","i64"]]'::jsonb
 WHEN 'resume' THEN '[[1,"version","u32"],[2,"binding","binding"],[3,"parent_receipt_hash","string"],[4,"resume_command_hash","string"],[5,"resume_hook_payload_hash","string"],[6,"command_completed_at_unix_nano","i64"],[7,"host_time_unix_nano","i64"],[8,"hook_completed_at_unix_nano","i64"],[9,"completed_at_unix_nano","i64"],[10,"parent_binding","binding"],[11,"parent_completed_at_unix_nano","i64"]]'::jsonb
 WHEN 'promotion' THEN '[[1,"binding","binding"],[2,"parent","receipt"]]'::jsonb
 WHEN 'binding' THEN '[[1,"protocol_version","u32"],[2,"token","string"],[3,"instance_id","string"],[4,"app_id","string"],[5,"deployment_id","string"],[6,"account_id","string"],[7,"node_id","string"],[8,"incarnation","string"],[9,"desired_revision","i64"],[10,"effective_hash","string"],[11,"captured_input_hash","string"],[12,"payload_hash","string"],[13,"egress_revision","i64"],[14,"issued_at_unix_nano","i64"],[15,"expires_at_unix_nano","i64"],[16,"artifact_sources_hash","string"],[17,"snapshot_capture_token","string"],[18,"snapshot_evidence_hash","string"]]'::jsonb
 END;
$$;
-- Retain the original receipt's entire deterministic protobuf representation.
CREATE FUNCTION application_standard_snapshot_resume_parent_hash(parent jsonb) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(convert_to('gregale.runtime-snapshot-resume.parent.v1','UTF8') || decode('00','hex') ||
  application_standard_restore_wire_message('receipt',parent)),'hex');
$$;

CREATE FUNCTION application_standard_snapshot_resume_parent(r jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT (r-'snapshot_resume_evidence') || jsonb_build_object('binding',r->'snapshot_resume_evidence'->'parent_binding',
  'paused',true,'completed_at_unix_nano',r->'snapshot_resume_evidence'->'parent_completed_at_unix_nano');
$$;

CREATE FUNCTION application_standard_snapshot_resume_valid(b jsonb,r jsonb,parent jsonb,now_nano bigint) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE e jsonb:=r->'snapshot_resume_evidence'; old jsonb:=parent->'binding'; command_nano bigint; host_nano bigint;
 hook_nano bigint; completed_nano bigint; parent_nano bigint; payload text;
BEGIN
 IF (jsonb_typeof(e)='object' AND e-ARRAY['version','binding','parent_receipt_hash','resume_command_hash','resume_hook_payload_hash',
    'command_completed_at_unix_nano','host_time_unix_nano','hook_completed_at_unix_nano','completed_at_unix_nano','parent_binding','parent_completed_at_unix_nano']='{}'::jsonb
  AND (SELECT count(*) FROM jsonb_object_keys(e))=11
  AND e->'version'='1'::jsonb AND jsonb_typeof(e->'version')='number' AND e->>'version'='1'
  AND r-ARRAY['binding','native_input_hash','netns','host_ip','lease_uid','method','paused','completed_at_unix_nano','artifact_consumption','snapshot_consumption','snapshot_resume_evidence']='{}'::jsonb
  AND old-ARRAY['protocol_version','token','instance_id','app_id','deployment_id','account_id','node_id','incarnation','desired_revision','effective_hash','captured_input_hash','payload_hash','egress_revision','issued_at_unix_nano','expires_at_unix_nano','artifact_sources_hash','snapshot_capture_token','snapshot_evidence_hash']='{}'::jsonb
  AND old->'protocol_version'='2'::jsonb AND parent->'paused'='true'::jsonb AND parent->'method'='1'::jsonb
  AND NOT(parent ? 'snapshot_resume_evidence') AND r->'paused'='false'::jsonb AND r->'method'='1'::jsonb
  AND r->'binding'=b AND e->'binding'=b AND e->'parent_binding'=old
  AND e->'parent_completed_at_unix_nano'=parent->'completed_at_unix_nano'
  AND (r-ARRAY['binding','paused','completed_at_unix_nano','snapshot_resume_evidence'])=(parent-ARRAY['binding','paused','completed_at_unix_nano'])
  AND (b-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano'])=(old-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano'])
  AND b->>'token'<>old->>'token' AND old->>'token' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND old->>'token'<>'00000000-0000-0000-0000-000000000000' AND old->>'payload_hash' ~ '^[0-9a-f]{64}$'
  AND e->>'parent_receipt_hash'=application_standard_snapshot_resume_parent_hash(parent)
  AND e->>'resume_command_hash'=encode(sha256(convert_to('gregale.runtime-resume.command.v1','UTF8') || decode('00','hex') || convert_to('{"state":"Resumed"}','UTF8')),'hex')
  AND e->>'resume_hook_payload_hash' ~ '^[0-9a-f]{64}$'
  AND e->'completed_at_unix_nano'=r->'completed_at_unix_nano') IS NOT TRUE THEN RETURN false; END IF;
 -- Enforce integer/bool/string wire types, including nested bindings, before casts.
 PERFORM application_standard_restore_wire_message('receipt',r);
 PERFORM application_standard_restore_wire_message('receipt',parent);
 command_nano:=(e->>'command_completed_at_unix_nano')::bigint; host_nano:=(e->>'host_time_unix_nano')::bigint;
 hook_nano:=(e->>'hook_completed_at_unix_nano')::bigint; completed_nano:=(e->>'completed_at_unix_nano')::bigint;
 parent_nano:=(parent->>'completed_at_unix_nano')::bigint;
 payload:=encode(sha256(convert_to('gregale.runtime-promotion.v1','UTF8') || decode('00','hex') ||
  application_standard_restore_wire_message('promotion',jsonb_build_object('parent',parent))),'hex');
 RETURN coalesce(b->>'payload_hash'=payload AND parent_nano>0 AND (old->>'issued_at_unix_nano')::bigint>0
  AND (old->>'expires_at_unix_nano')::numeric>(old->>'issued_at_unix_nano')::numeric
  AND (old->>'expires_at_unix_nano')::numeric-(old->>'issued_at_unix_nano')::numeric<=600000000000
  AND parent_nano::numeric>=(old->>'issued_at_unix_nano')::numeric-5000000000
  AND parent_nano<(old->>'expires_at_unix_nano')::bigint
  AND command_nano>0 AND command_nano>=parent_nano AND command_nano::numeric>=(b->>'issued_at_unix_nano')::numeric-5000000000
  AND host_nano>=command_nano AND hook_nano>=host_nano AND completed_nano>=hook_nano
  AND completed_nano<(b->>'expires_at_unix_nano')::bigint AND completed_nano::numeric<=now_nano::numeric+5000000000,false);
EXCEPTION WHEN data_exception OR check_violation THEN RETURN false;
END;
$$;

-- Boot publication can never stand in for the distinct issued promotion.
CREATE FUNCTION application_standard_snapshot_resume_boot_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.receipt ? 'snapshot_resume_evidence' THEN
  RAISE EXCEPTION 'resume requires an issued promotion' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_snapshot_resume_boot_guard BEFORE INSERT OR UPDATE OF receipt ON instance_application_standard_boots
 FOR EACH ROW EXECUTE FUNCTION application_standard_snapshot_resume_boot_guard();
CREATE OR REPLACE FUNCTION application_standard_native_artifact_protocol(b jsonb,r jsonb,input jsonb,protocol smallint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE version integer; actual text; captured jsonb; paused_load boolean; parent jsonb;
BEGIN
 IF jsonb_typeof(b->'protocol_version') IS DISTINCT FROM 'number' OR b->>'protocol_version' NOT IN ('1','2') THEN
  RAISE EXCEPTION 'native protocol is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 version:=(b->>'protocol_version')::integer;
 IF version>protocol THEN
  RAISE EXCEPTION 'native protocol is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version<>2 AND EXISTS(SELECT 1 FROM jsonb_array_elements(input->'runtime_artifacts'->'artifacts') a
  WHERE a->>'kind' IN ('source-app-layer','function-layer')) THEN
  RAISE EXCEPTION 'source native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF coalesce((input->>'persisted_revision')::bigint,0)>0 AND input->'adoptions'='[]'::jsonb
  AND input->'materialized_fields'='[]'::jsonb AND version<>2 THEN
  RAISE EXCEPTION 'retained native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version=1 THEN
  IF coalesce(b->>'artifact_sources_hash','')<>'' OR (r IS NOT NULL AND (r ? 'artifact_consumption' OR r ? 'snapshot_consumption' OR r ? 'snapshot_resume_evidence')) THEN
   RAISE EXCEPTION 'legacy authority cannot acknowledge consumed artifacts' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  RETURN;
 END IF;
 actual:=application_standard_native_source_hash(input->'runtime_artifacts'->'artifacts');
 IF jsonb_typeof(b->'artifact_sources_hash') IS DISTINCT FROM 'string' OR b->>'artifact_sources_hash' IS DISTINCT FROM actual THEN
  RAISE EXCEPTION 'native source hash differs from captured producers' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF r IS NULL THEN RETURN; END IF;
 IF (jsonb_typeof(r->'method')='number' AND r->>'method' IN ('0','1')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native method is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF (jsonb_typeof(r->'paused')='boolean' AND application_standard_native_consumption_valid(r->'artifact_consumption',actual)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native consumed artifact receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF r->'method'='0'::jsonb AND r->'paused'='false'::jsonb AND NOT (r ? 'snapshot_consumption' OR r ? 'snapshot_resume_evidence') THEN RETURN; END IF;
 paused_load:=r->'paused'='true'::jsonb OR r ? 'snapshot_resume_evidence';
 IF (r->'method'='1'::jsonb AND r ? 'snapshot_consumption'
  AND r-ARRAY['binding','native_input_hash','netns','host_ip','lease_uid','method','paused','completed_at_unix_nano','artifact_consumption','snapshot_consumption','snapshot_resume_evidence']='{}'::jsonb
  AND r->'artifact_consumption'->>'config_hash'=encode(sha256(convert_to('{"mem_backend":{"backend_path":"snap-in-mem","backend_type":"File"},"resume_vm":' || CASE WHEN paused_load THEN 'false' ELSE 'true' END || ',"snapshot_path":"snap-in-vmstate"}','UTF8')),'hex')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native restore receipt is incomplete' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF r ? 'snapshot_resume_evidence' THEN
  parent:=application_standard_snapshot_resume_parent(r);
  IF NOT application_standard_snapshot_resume_valid(b,r,parent,(r->>'completed_at_unix_nano')::bigint) THEN
   RAISE EXCEPTION 'native resume lineage is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 SELECT c.acknowledgment->'capture' INTO captured FROM application_standard_snapshot_captures c
 WHERE c.token::text=b->>'snapshot_capture_token' AND c.account_id::text=b->>'account_id' AND c.app_id::text=b->>'app_id'
  AND c.deployment_id::text=b->>'deployment_id' AND c.acknowledgment IS NOT NULL AND c.received_at IS NOT NULL FOR SHARE NOWAIT;
 IF NOT FOUND OR NOT application_standard_snapshot_consumption_matches(r->'snapshot_consumption',b,captured,r->'artifact_consumption') THEN
  RAISE EXCEPTION 'native restore backing differs from catalog' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
END;
$$;
CREATE OR REPLACE FUNCTION application_standard_native_promotion_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE locked jsonb; parent jsonb; b jsonb; r jsonb; now_nano bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'native promotion history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF NEW.token IS DISTINCT FROM OLD.token OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
   OR NEW.parent_token IS DISTINCT FROM OLD.parent_token OR NEW.binding IS DISTINCT FROM OLD.binding
   OR NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.receipt IS NOT NULL OR NEW.receipt IS NULL THEN
   RAISE EXCEPTION 'native promotion history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.receipt IS NOT NULL THEN
  RAISE EXCEPTION 'promotion receipt requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_native_promotion(NEW.instance_id,false);
 parent:=locked->'parent'; b:=NEW.binding; now_nano:=(locked->>'clock_unix_nano')::bigint;
 IF parent->'binding'->>'token' IS DISTINCT FROM NEW.parent_token::text OR NEW.token=NEW.parent_token
  OR b->>'token' IS DISTINCT FROM NEW.token::text
  OR (b-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano']) IS DISTINCT FROM
     ((parent->'binding')-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano'])
  OR coalesce(b->>'payload_hash','') !~ '^[0-9a-f]{64}$'
  OR coalesce((b->>'issued_at_unix_nano')::bigint,0)<=0
  OR coalesce((b->>'expires_at_unix_nano')::bigint,0)<=now_nano
  OR (b->>'expires_at_unix_nano')::bigint <= (b->>'issued_at_unix_nano')::bigint
  OR (locked->>'artifact_expires_at_unix_nano' IS NOT NULL AND (b->>'expires_at_unix_nano')::bigint>(locked->>'artifact_expires_at_unix_nano')::bigint)
  OR (b->>'expires_at_unix_nano')::numeric-(b->>'issued_at_unix_nano')::numeric >600000000000
  OR (b->>'issued_at_unix_nano')::bigint>now_nano+5000000000
  OR (TG_OP='INSERT' AND (b->>'issued_at_unix_nano')::bigint<now_nano-5000000000) THEN
  RAISE EXCEPTION 'native promotion grant is stale or invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF b->'protocol_version'='2'::jsonb THEN
  PERFORM application_standard_lock_snapshot_restore(b,locked->'input_snapshot');
  IF b->>'payload_hash' IS DISTINCT FROM encode(sha256(convert_to('gregale.runtime-promotion.v1','UTF8') || decode('00','hex') ||
   application_standard_restore_wire_message('promotion',jsonb_build_object('parent',parent))),'hex') THEN
   RAISE EXCEPTION 'native promotion payload differs from parent' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR (r->>'paused')::boolean IS DISTINCT FROM false
   OR (r-ARRAY['binding','paused','completed_at_unix_nano','snapshot_resume_evidence']) IS DISTINCT FROM (parent-ARRAY['binding','paused','completed_at_unix_nano'])
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint<(parent->>'completed_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint >= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native promotion receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  IF b->'protocol_version'='2'::jsonb THEN
   IF NOT application_standard_snapshot_resume_valid(b,r,parent,now_nano) THEN
    RAISE EXCEPTION 'native measured resume proof is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
   END IF;
   PERFORM application_standard_native_artifact_protocol(b,r,locked->'input_snapshot',(locked->>'protocol_version')::smallint);
  ELSIF r ? 'snapshot_resume_evidence' THEN
   RAISE EXCEPTION 'legacy promotion cannot acknowledge measured resume' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$_$;
CREATE OR REPLACE FUNCTION application_standard_restore_publication_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE b jsonb; locked jsonb;
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','warm','migrating')
  OR NEW.application_standard_boot_token IS NULL
  OR (NEW.state NOT IN ('running','warm','migrating') AND coalesce(NEW.netns,'')=''
   AND NEW.host_ip IS NULL AND coalesce(NEW.guest_uid,0)=0)
  OR (NEW.application_standard_boot_token IS NOT DISTINCT FROM OLD.application_standard_boot_token AND NEW.application_standard_promotion_token IS NOT DISTINCT FROM OLD.application_standard_promotion_token AND OLD.state NOT IN ('waking','cold_booting')) THEN RETURN NEW; END IF;
 SELECT binding INTO b FROM instance_application_standard_boots
  WHERE token=NEW.application_standard_boot_token AND instance_id=NEW.id FOR SHARE NOWAIT;
 IF NEW.application_standard_promotion_token IS NOT NULL THEN
  SELECT binding INTO b FROM instance_application_standard_promotions
   WHERE token=NEW.application_standard_promotion_token AND instance_id=NEW.id FOR SHARE NOWAIT;
 END IF;
 IF coalesce(b->>'snapshot_capture_token','')='' AND coalesce(b->>'snapshot_evidence_hash','')='' THEN RETURN NEW; END IF;
 locked:=application_standard_lock_native_boot(OLD.id,OLD.state);
 PERFORM application_standard_lock_snapshot_restore(b,locked->'input_snapshot');
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'restore publication is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- Retain measured resume and publication fences on binary rollback.
