-- filename: 20261005205741272_application_standard_snapshot_consumption.sql
-- adr: 595. Measured restore is distinct from verified cold fallback.
-- Existing grants, captures and migration bytes remain unchanged.
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
 WHEN 'receipt' THEN '[[1,"binding","binding"],[2,"native_input_hash","string"],[3,"netns","string"],[4,"host_ip","string"],[5,"lease_uid","i32"],[6,"method","i32"],[7,"paused","bool"],[8,"completed_at_unix_nano","i64"],[9,"artifact_consumption","consumption"],[10,"snapshot_consumption","snapshot-consumption"]]'::jsonb
 WHEN 'snapshot-consumption' THEN '[[1,"version","u32"],[2,"capture_token","string"],[3,"evidence_hash","string"],[4,"memory","artifact"],[5,"vmstate","artifact"],[6,"private_drive","artifact"],[7,"mapped_memory_bytes","i64"]]'::jsonb
 WHEN 'binding' THEN '[[1,"protocol_version","u32"],[2,"token","string"],[3,"instance_id","string"],[4,"app_id","string"],[5,"deployment_id","string"],[6,"account_id","string"],[7,"node_id","string"],[8,"incarnation","string"],[9,"desired_revision","i64"],[10,"effective_hash","string"],[11,"captured_input_hash","string"],[12,"payload_hash","string"],[13,"egress_revision","i64"],[14,"issued_at_unix_nano","i64"],[15,"expires_at_unix_nano","i64"],[16,"artifact_sources_hash","string"],[17,"snapshot_capture_token","string"],[18,"snapshot_evidence_hash","string"]]'::jsonb
 END;
$$;


CREATE OR REPLACE FUNCTION application_standard_snapshot_consumption_matches(c jsonb,b jsonb,capture jsonb,drives jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 RETURN coalesce(jsonb_typeof(c)='object'
  AND c-ARRAY['version','capture_token','evidence_hash','memory','vmstate','private_drive','mapped_memory_bytes']='{}'::jsonb
  AND jsonb_typeof(c->'version')='number' AND c->>'version'='1' AND b->'protocol_version'='2'::jsonb
  AND jsonb_typeof(c->'capture_token')='string' AND c->'capture_token'=b->'snapshot_capture_token'
  AND c->>'capture_token' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND c->>'capture_token'<>'00000000-0000-0000-0000-000000000000'
  AND jsonb_typeof(c->'evidence_hash')='string' AND c->'evidence_hash'=b->'snapshot_evidence_hash'
  AND c->>'evidence_hash' ~ '^[0-9a-f]{64}$'
  AND c->'memory'=capture->'memory' AND c->'vmstate'=capture->'vmstate' AND c->'private_drive'=capture->'private_drive'
  AND jsonb_typeof(c->'mapped_memory_bytes')='number' AND c->>'mapped_memory_bytes' ~ '^[1-9][0-9]{0,10}$'
  AND c->'mapped_memory_bytes'=c->'memory'->'bytes'
  AND (c->>'mapped_memory_bytes')::bigint%1048576=0
  AND EXISTS(SELECT 1 FROM jsonb_array_elements(drives->'drives') d
   WHERE application_standard_native_source_role(d->'source')='main' AND d->'injected_bytes'=c->'private_drive'->'bytes'),false);
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value THEN RETURN false;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_artifact_protocol(b jsonb,r jsonb,input jsonb,protocol smallint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE version integer; actual text; captured jsonb;
BEGIN
 IF jsonb_typeof(b->'protocol_version') IS DISTINCT FROM 'number' OR b->>'protocol_version' NOT IN ('1','2') THEN
  RAISE EXCEPTION 'native protocol is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 version:=(b->>'protocol_version')::integer;
 IF version>protocol THEN
  RAISE EXCEPTION 'native protocol is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version=1 THEN
  IF coalesce(b->>'artifact_sources_hash','')<>'' OR (r IS NOT NULL AND (r ? 'artifact_consumption' OR r ? 'snapshot_consumption')) THEN
   RAISE EXCEPTION 'legacy authority cannot acknowledge consumed artifacts' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  RETURN;
 END IF;
 actual:=application_standard_native_source_hash(input->'runtime_artifacts'->'artifacts');
 IF jsonb_typeof(b->'artifact_sources_hash') IS DISTINCT FROM 'string' OR b->>'artifact_sources_hash' IS DISTINCT FROM actual THEN
  RAISE EXCEPTION 'native source hash differs from captured producers' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF r IS NULL THEN RETURN; END IF;
 IF (r->'paused'='false'::jsonb AND application_standard_native_consumption_valid(r->'artifact_consumption',actual)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native consumed artifact receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF r->'method'='0'::jsonb AND NOT (r ? 'snapshot_consumption') THEN RETURN; END IF;
 IF (r->'method'='1'::jsonb AND r ? 'snapshot_consumption'
  AND r-ARRAY['binding','native_input_hash','netns','host_ip','lease_uid','method','paused','completed_at_unix_nano','artifact_consumption','snapshot_consumption']='{}'::jsonb
  AND r->'artifact_consumption'->>'config_hash'=encode(sha256(convert_to('{"mem_backend":{"backend_path":"snap-in-mem","backend_type":"File"},"resume_vm":true,"snapshot_path":"snap-in-vmstate"}','UTF8')),'hex')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native restore receipt is incomplete' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 SELECT c.acknowledgment->'capture' INTO captured FROM application_standard_snapshot_captures c
 WHERE c.token::text=b->>'snapshot_capture_token' AND c.account_id::text=b->>'account_id' AND c.app_id::text=b->>'app_id'
  AND c.deployment_id::text=b->>'deployment_id' AND c.acknowledgment IS NOT NULL AND c.received_at IS NOT NULL FOR SHARE NOWAIT;
 IF NOT FOUND OR NOT application_standard_snapshot_consumption_matches(r->'snapshot_consumption',b,captured,r->'artifact_consumption') THEN
  RAISE EXCEPTION 'native restore backing differs from catalog' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- Retain fail-closed receipt verification on binary rollback.
