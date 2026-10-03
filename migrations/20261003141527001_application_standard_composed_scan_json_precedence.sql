-- +goose Up
-- adr: 435. Parenthesize the facts object before subtracting its allowed keys.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_artifact_deadline(input jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE identity jsonb:=input->'runtime_artifacts'; artifact jsonb; s deployment_runtime_scans%ROWTYPE;
 deadline timestamptz; enforce boolean:=input->'settings'->>'security_policy'='enforce'; source_hash text;
BEGIN
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed producer evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN NULL;
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 source_hash:=application_standard_native_source_hash(identity->'artifacts');
 IF (SELECT sum((a->>'bytes')::bigint) FROM jsonb_array_elements(identity->'artifacts') a)>34359738368 THEN
  RAISE EXCEPTION 'native runtime sources exceed bounds' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT scan.* INTO s FROM deployment_runtime_scan_current c JOIN deployment_runtime_scans scan ON scan.id=c.scan_id
 WHERE c.deployment_id=(identity->>'deployment_id')::uuid FOR SHARE OF scan NOWAIT;
 IF (s.input_snapshot-ARRAY['facts','status','reports','failure']=identity AND s.input_snapshot->>'status'='complete'
  AND jsonb_typeof(s.input_snapshot->'facts'->'version')='number' AND s.input_snapshot->'facts'->>'version'='1'
  AND (s.input_snapshot->'facts')-ARRAY['version','input_hash','sources_hash','views']='{}'::jsonb
  AND s.input_snapshot->'facts'->>'input_hash'=application_standard_runtime_identity_hash(identity)
  AND s.input_snapshot->'facts'->>'sources_hash'=source_hash AND s.scanned_at<=now_utc AND s.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed scan missing or stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 deadline:=least(s.expires_at,application_standard_native_composed_views_deadline(s.input_snapshot,identity,now_utc,enforce));
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN PERFORM application_standard_native_base_producer_current(artifact,now_utc);
  ELSE deadline:=least(deadline,application_standard_native_producer_deadline(input,artifact,now_utc)); END IF;
 END LOOP;
 IF deadline<=clock_timestamp() THEN
  RAISE EXCEPTION 'native composed authority expired during read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN deadline;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native composed inputs busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value OR datetime_field_overflow THEN
 RAISE EXCEPTION 'native composed inputs invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_artifact_deadline(input jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE identity jsonb:=input->'runtime_artifacts'; artifact jsonb; s deployment_runtime_scans%ROWTYPE;
 deadline timestamptz; enforce boolean:=input->'settings'->>'security_policy'='enforce'; source_hash text;
BEGIN
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed producer evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN NULL;
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 source_hash:=application_standard_native_source_hash(identity->'artifacts');
 IF (SELECT sum((a->>'bytes')::bigint) FROM jsonb_array_elements(identity->'artifacts') a)>34359738368 THEN
  RAISE EXCEPTION 'native runtime sources exceed bounds' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT scan.* INTO s FROM deployment_runtime_scan_current c JOIN deployment_runtime_scans scan ON scan.id=c.scan_id
 WHERE c.deployment_id=(identity->>'deployment_id')::uuid FOR SHARE OF scan NOWAIT;
 IF (s.input_snapshot-ARRAY['facts','status','reports','failure']=identity AND s.input_snapshot->>'status'='complete'
  AND jsonb_typeof(s.input_snapshot->'facts'->'version')='number' AND s.input_snapshot->'facts'->>'version'='1'
  AND s.input_snapshot->'facts'-ARRAY['version','input_hash','sources_hash','views']='{}'::jsonb
  AND s.input_snapshot->'facts'->>'input_hash'=application_standard_runtime_identity_hash(identity)
  AND s.input_snapshot->'facts'->>'sources_hash'=source_hash AND s.scanned_at<=now_utc AND s.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed scan missing or stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 deadline:=least(s.expires_at,application_standard_native_composed_views_deadline(s.input_snapshot,identity,now_utc,enforce));
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN PERFORM application_standard_native_base_producer_current(artifact,now_utc);
  ELSE deadline:=least(deadline,application_standard_native_producer_deadline(input,artifact,now_utc)); END IF;
 END LOOP;
 IF deadline<=clock_timestamp() THEN
  RAISE EXCEPTION 'native composed authority expired during read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN deadline;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native composed inputs busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value OR datetime_field_overflow THEN
 RAISE EXCEPTION 'native composed inputs invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;
-- +goose StatementEnd
