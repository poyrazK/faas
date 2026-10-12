-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_base_deadline(artifact jsonb, now_utc timestamp with time zone, enforce boolean) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
DECLARE p base_image_producers%ROWTYPE; s base_image_scans%ROWTYPE;
BEGIN
 IF NOT pg_try_advisory_xact_lock_shared(hashtextextended('gregale.base-producer.' || (artifact->>'storage_key'),0)) THEN
  RAISE EXCEPTION 'native base approval is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT b.* INTO p FROM base_image_producer_current c JOIN base_image_producers b ON b.id=c.producer_id
 WHERE c.storage_key=artifact->>'storage_key' FOR SHARE OF b NOWAIT;
 SELECT scan.* INTO s FROM base_image_scan_current c JOIN base_image_scans scan ON scan.id=c.scan_id
 WHERE c.storage_key=p.storage_key FOR SHARE OF scan NOWAIT;
 IF (p.id::text=artifact->>'producer_id' AND p.input_hash=artifact->>'producer_hash'
  AND p.input_snapshot->'artifact'=jsonb_build_object('storage_key',artifact->>'storage_key','digest',artifact->>'digest','bytes',artifact->'bytes')
  AND s.base_producer_id=p.id AND s.input_snapshot->>'base_input_hash'=p.input_hash
  AND s.input_snapshot->'artifact'=p.input_snapshot->'artifact'
  AND s.input_snapshot->>'source_reference'=p.input_snapshot->>'source_reference'
  AND s.scanned_at>=p.published_at AND p.published_at<=now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native base scan selection changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN application_standard_native_scan_deadline(s.input_snapshot,s.scanned_at,s.expires_at,now_utc,enforce);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_base_producer_current(artifact jsonb, now_utc timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE p base_image_producers%ROWTYPE;
BEGIN
 IF NOT pg_try_advisory_xact_lock_shared(hashtextextended('gregale.base-producer.' || (artifact->>'storage_key'),0)) THEN
  RAISE EXCEPTION 'native runtime base is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT b.* INTO p FROM base_image_producer_current c JOIN base_image_producers b ON b.id=c.producer_id
 WHERE c.storage_key=artifact->>'storage_key' FOR SHARE OF b NOWAIT;
 IF (p.id::text=artifact->>'producer_id' AND p.input_hash=artifact->>'producer_hash' AND p.published_at<=now_utc
  AND p.input_snapshot->'artifact'=jsonb_build_object('storage_key',artifact->>'storage_key','digest',artifact->>'digest','bytes',artifact->'bytes')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime base changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_runtime_base_producer(root_producer jsonb) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE p base_image_producers%ROWTYPE; selected uuid;
BEGIN
 IF NOT root_producer ? 'base_producer_id' THEN RETURN NULL; END IF;
 SELECT * INTO p FROM base_image_producers WHERE id=(root_producer->>'base_producer_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND OR p.input_hash IS DISTINCT FROM root_producer->>'base_input_hash' THEN
  RAISE EXCEPTION 'runtime base producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock_shared(hashtextextended('gregale.base-producer.' || p.storage_key,0)) THEN
  RAISE EXCEPTION 'runtime base producer is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT producer_id INTO selected FROM base_image_producer_current WHERE storage_key=p.storage_key FOR SHARE NOWAIT;
 IF selected IS DISTINCT FROM p.id OR p.storage_key IS NOT DISTINCT FROM root_producer->>'storage_key' THEN
  RAISE EXCEPTION 'runtime base producer selection changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN jsonb_build_object('kind','base-image','workload_name','','producer_id',p.id::text,'producer_hash',p.input_hash,
  'storage_key',p.storage_key,'digest',p.input_snapshot->'artifact'->>'digest','bytes',(p.input_snapshot->'artifact'->>'bytes')::bigint);
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_base_deadline(artifact jsonb, now_utc timestamp with time zone, enforce boolean) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
DECLARE p base_image_producers%ROWTYPE; s base_image_scans%ROWTYPE;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.base-producer.' || (artifact->>'storage_key'),0)) THEN
  RAISE EXCEPTION 'native base approval is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT b.* INTO p FROM base_image_producer_current c JOIN base_image_producers b ON b.id=c.producer_id
 WHERE c.storage_key=artifact->>'storage_key' FOR SHARE OF b NOWAIT;
 SELECT scan.* INTO s FROM base_image_scan_current c JOIN base_image_scans scan ON scan.id=c.scan_id
 WHERE c.storage_key=p.storage_key FOR SHARE OF scan NOWAIT;
 IF (p.id::text=artifact->>'producer_id' AND p.input_hash=artifact->>'producer_hash'
  AND p.input_snapshot->'artifact'=jsonb_build_object('storage_key',artifact->>'storage_key','digest',artifact->>'digest','bytes',artifact->'bytes')
  AND s.base_producer_id=p.id AND s.input_snapshot->>'base_input_hash'=p.input_hash
  AND s.input_snapshot->'artifact'=p.input_snapshot->'artifact'
  AND s.input_snapshot->>'source_reference'=p.input_snapshot->>'source_reference'
  AND s.scanned_at>=p.published_at AND p.published_at<=now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native base scan selection changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN application_standard_native_scan_deadline(s.input_snapshot,s.scanned_at,s.expires_at,now_utc,enforce);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_base_producer_current(artifact jsonb, now_utc timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE p base_image_producers%ROWTYPE;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.base-producer.' || (artifact->>'storage_key'),0)) THEN
  RAISE EXCEPTION 'native runtime base is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT b.* INTO p FROM base_image_producer_current c JOIN base_image_producers b ON b.id=c.producer_id
 WHERE c.storage_key=artifact->>'storage_key' FOR SHARE OF b NOWAIT;
 IF (p.id::text=artifact->>'producer_id' AND p.input_hash=artifact->>'producer_hash' AND p.published_at<=now_utc
  AND p.input_snapshot->'artifact'=jsonb_build_object('storage_key',artifact->>'storage_key','digest',artifact->>'digest','bytes',artifact->'bytes')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime base changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_runtime_base_producer(root_producer jsonb) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE p base_image_producers%ROWTYPE; selected uuid;
BEGIN
 IF NOT root_producer ? 'base_producer_id' THEN RETURN NULL; END IF;
 SELECT * INTO p FROM base_image_producers WHERE id=(root_producer->>'base_producer_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND OR p.input_hash IS DISTINCT FROM root_producer->>'base_input_hash' THEN
  RAISE EXCEPTION 'runtime base producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.base-producer.' || p.storage_key,0)) THEN
  RAISE EXCEPTION 'runtime base producer is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT producer_id INTO selected FROM base_image_producer_current WHERE storage_key=p.storage_key FOR SHARE NOWAIT;
 IF selected IS DISTINCT FROM p.id OR p.storage_key IS NOT DISTINCT FROM root_producer->>'storage_key' THEN
  RAISE EXCEPTION 'runtime base producer selection changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN jsonb_build_object('kind','base-image','workload_name','','producer_id',p.id::text,'producer_hash',p.input_hash,
  'storage_key',p.storage_key,'digest',p.input_snapshot->'artifact'->>'digest','bytes',(p.input_snapshot->'artifact'->>'bytes')::bigint);
END;
$$;
-- +goose StatementEnd
