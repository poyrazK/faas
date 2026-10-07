-- filename: 20261005211816538_application_standard_snapshot_consumption_capabilities.sql
-- adr: 595. Restore the source/retained capability guards from the canonical schema.
-- The preceding applied migration remains frozen. Require canonical method scalars.
-- +goose Up
-- +goose StatementBegin
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
 IF version<>2 AND EXISTS(SELECT 1 FROM jsonb_array_elements(input->'runtime_artifacts'->'artifacts') a
  WHERE a->>'kind' IN ('source-app-layer','function-layer')) THEN
  RAISE EXCEPTION 'source native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF coalesce((input->>'persisted_revision')::bigint,0)>0 AND input->'adoptions'='[]'::jsonb
  AND input->'materialized_fields'='[]'::jsonb AND version<>2 THEN
  RAISE EXCEPTION 'retained native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
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
 IF (jsonb_typeof(r->'method')='number' AND r->>'method' IN ('0','1')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native method is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
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
-- Retain source, retained-byte and snapshot receipt fences on binary rollback.
