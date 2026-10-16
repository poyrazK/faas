-- filename: 20261003233525610_application_standard_source_native_protocol.sql
-- adr: 435. Source native authority requires consumed-byte protocol 2; retained registry authority is unchanged.
-- +goose Up
-- +goose StatementBegin
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
 IF version<>2 AND EXISTS(SELECT 1 FROM jsonb_array_elements(input->'runtime_artifacts'->'artifacts') a
  WHERE a->>'kind' IN ('source-app-layer','function-layer')) THEN
  RAISE EXCEPTION 'source native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
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
DO $$ BEGIN RAISE WARNING 'source native consumed-byte protocol requirement is retained on Down'; END $$;
-- +goose StatementEnd
