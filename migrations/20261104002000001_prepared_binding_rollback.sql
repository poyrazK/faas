-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Prepared database rollback targets retain the same evidence and policy fences.
CREATE OR REPLACE FUNCTION authorize_binding_release_traffic(fences jsonb) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
DECLARE f jsonb; observed text; d deployments%ROWTYPE; p app_binding_release_policies%ROWTYPE;
BEGIN
 FOR f IN SELECT value FROM jsonb_array_elements(fences) LOOP
  SELECT r.epoch::text||':'||r.revision::text INTO observed FROM app_binding_promotion_revisions r
  JOIN apps a ON a.id=r.app_id WHERE r.app_id=(f->>'app_id')::uuid AND a.account_id=(f->>'account_id')::uuid AND a.status<>'deleted'
  FOR UPDATE OF r;
  IF observed IS NULL OR observed<>f->>'revision' THEN
   RAISE EXCEPTION 'binding facts changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  SELECT * INTO d FROM deployments WHERE id=(f->>'deployment_id')::uuid AND app_id=(f->>'app_id')::uuid;
  IF d.id IS NULL OR (d.status<>'live' AND NOT (d.status='snapshotting' AND d.traffic_percent=0 AND d.rollback_prepared_at IS NOT NULL)) OR d.scope<>f->>'scope' OR coalesce(d.rootfs_key,'')='' OR coalesce(d.image_digest,'')='' THEN
   RAISE EXCEPTION 'binding deployment changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  IF NULLIF(f->>'valid_until','')::timestamptz <= clock_timestamp() THEN
   RAISE EXCEPTION 'binding evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
  SELECT * INTO p FROM app_binding_release_policies WHERE app_id=d.app_id AND scope=d.scope;
  IF p.mode='enforce' AND NOT coalesce((f->>'policy_revision')::bigint=p.revision AND (f->>'max_age_seconds')::numeric>0 AND (f->>'max_age_seconds')::numeric<=p.max_age_seconds AND NOT (f->>'allow_unsupported')::boolean AND (NOT p.require_application_ack OR (f->>'require_application_ack')::boolean) AND coalesce(NULLIF(f->'object_storage_requirements','null'::jsonb),'{}'::jsonb)=p.object_storage_requirements AND coalesce(NULLIF(f->'queue_requirements','null'::jsonb),'{}'::jsonb)=p.queue_requirements,false) THEN
   RAISE EXCEPTION 'binding release policy changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 PERFORM set_config('gregale.binding_release_fences',fences::text,true);
 RETURN true;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authorize_binding_release_traffic(fences jsonb) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
DECLARE f jsonb; observed text; d deployments%ROWTYPE; p app_binding_release_policies%ROWTYPE;
BEGIN
 FOR f IN SELECT value FROM jsonb_array_elements(fences) LOOP
  SELECT r.epoch::text||':'||r.revision::text INTO observed FROM app_binding_promotion_revisions r
  JOIN apps a ON a.id=r.app_id WHERE r.app_id=(f->>'app_id')::uuid AND a.account_id=(f->>'account_id')::uuid AND a.status<>'deleted'
  FOR UPDATE OF r;
  IF observed IS NULL OR observed<>f->>'revision' THEN
   RAISE EXCEPTION 'binding facts changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  SELECT * INTO d FROM deployments WHERE id=(f->>'deployment_id')::uuid AND app_id=(f->>'app_id')::uuid;
  IF d.id IS NULL OR d.status<>'live' OR d.scope<>f->>'scope' OR coalesce(d.rootfs_key,'')='' OR coalesce(d.image_digest,'')='' THEN
   RAISE EXCEPTION 'binding deployment changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  IF NULLIF(f->>'valid_until','')::timestamptz <= clock_timestamp() THEN
   RAISE EXCEPTION 'binding evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
  SELECT * INTO p FROM app_binding_release_policies WHERE app_id=d.app_id AND scope=d.scope;
  IF p.mode='enforce' AND NOT coalesce((f->>'policy_revision')::bigint=p.revision AND (f->>'max_age_seconds')::numeric>0 AND (f->>'max_age_seconds')::numeric<=p.max_age_seconds AND NOT (f->>'allow_unsupported')::boolean AND (NOT p.require_application_ack OR (f->>'require_application_ack')::boolean) AND coalesce(NULLIF(f->'object_storage_requirements','null'::jsonb),'{}'::jsonb)=p.object_storage_requirements AND coalesce(NULLIF(f->'queue_requirements','null'::jsonb),'{}'::jsonb)=p.queue_requirements,false) THEN
   RAISE EXCEPTION 'binding release policy changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 PERFORM set_config('gregale.binding_release_fences',fences::text,true);
 RETURN true;
END $$;
-- +goose StatementEnd
