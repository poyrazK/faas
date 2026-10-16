-- filename: 20261002192949557_application_standard_consumption_integer_fence.sql
-- adr: 431. Ensure saved measured receipts decode as native integer facts.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_consumed_drive_valid(d jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE a jsonb; role text;
BEGIN
 a:=d->'source'; role:=application_standard_native_source_role(a);
 RETURN coalesce(jsonb_typeof(d)='object' AND role IS NOT NULL
  AND d-ARRAY['source','drive_id','read_only','root_device','producer_digest','producer_bytes','injected_digest','injected_bytes']='{}'::jsonb
  AND a-ARRAY['kind','workload_name','storage_key','digest','bytes']='{}'::jsonb
  AND jsonb_typeof(d->'drive_id')='string' AND octet_length(d->>'drive_id') BETWEEN 1 AND 512
  AND position(chr(13) IN (d->>'drive_id'))=0 AND position(chr(10) IN (d->>'drive_id'))=0
  AND jsonb_typeof(d->'read_only')='boolean' AND d->'read_only'=to_jsonb(role<>'main')
  AND jsonb_typeof(d->'root_device')='boolean' AND d->'root_device'=to_jsonb(role='base')
  AND d->'producer_digest'=a->'digest' AND jsonb_typeof(d->'producer_bytes')='number'
  AND d->>'producer_bytes'=a->>'bytes'
  AND jsonb_typeof(d->'injected_bytes')='number' AND d->>'injected_bytes'=a->>'bytes' AND jsonb_typeof(d->'injected_digest')='string'
  AND d->>'injected_digest' ~ '^sha256:[0-9a-f]{64}$'
  AND (role='main' OR d->'injected_digest'=d->'producer_digest'),false);
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_artifact_protocol(b jsonb, r jsonb, input jsonb, protocol smallint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE version integer; actual text;
BEGIN
 IF jsonb_typeof(b->'protocol_version') IS DISTINCT FROM 'number' OR b->>'protocol_version' NOT IN ('1','2') THEN
  RAISE EXCEPTION 'native protocol is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 version:=(b->>'protocol_version')::integer;
 IF version NOT IN (1,2) OR version>protocol THEN
  RAISE EXCEPTION 'native protocol is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version=1 THEN
  IF coalesce(b->>'artifact_sources_hash','')<>'' OR (r IS NOT NULL AND r ? 'artifact_consumption') THEN
   RAISE EXCEPTION 'legacy authority cannot acknowledge consumed artifacts' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  RETURN;
 END IF;
 actual:=application_standard_native_source_hash(input->'runtime_artifacts'->'artifacts');
 IF jsonb_typeof(b->'artifact_sources_hash') IS DISTINCT FROM 'string' OR b->>'artifact_sources_hash' IS DISTINCT FROM actual THEN
  RAISE EXCEPTION 'native source hash differs from captured producers' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF r IS NOT NULL AND (jsonb_typeof(r->'method')='number' AND r->>'method'='0' AND r->'paused'='false'::jsonb
  AND application_standard_native_consumption_valid(r->'artifact_consumption',actual)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native consumed artifact receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE WARNING 'native consumed artifact integer fences are retained on Down'; END $$;
-- +goose StatementEnd
