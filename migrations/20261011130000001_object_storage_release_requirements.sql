-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_object_storage_release_requirements(requirements jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE entry record; count integer:=0;
BEGIN
 IF requirements IS NULL OR jsonb_typeof(requirements)<>'object' THEN RETURN false; END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(requirements) LOOP
  count:=count+1;
  IF count>32 OR entry.key !~ '^[A-Z][A-Z0-9_]{0,47}$'
   OR jsonb_typeof(entry.value)<>'string' OR entry.value #>> '{}' NOT IN ('read','write','read_write') THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
ALTER TABLE app_binding_release_policies ADD COLUMN IF NOT EXISTS object_storage_requirements jsonb NOT NULL DEFAULT '{}';
ALTER TABLE app_binding_release_policies DROP CONSTRAINT IF EXISTS object_storage_release_requirements;
ALTER TABLE app_binding_release_policies ADD CONSTRAINT object_storage_release_requirements CHECK(valid_object_storage_release_requirements(object_storage_requirements));
CREATE OR REPLACE FUNCTION authorize_binding_release_traffic(fences jsonb) RETURNS boolean LANGUAGE plpgsql AS $$
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
  IF p.mode='enforce' AND NOT coalesce((f->>'policy_revision')::bigint=p.revision AND (f->>'max_age_seconds')::numeric>0 AND (f->>'max_age_seconds')::numeric<=p.max_age_seconds AND NOT (f->>'allow_unsupported')::boolean AND (NOT p.require_application_ack OR (f->>'require_application_ack')::boolean) AND coalesce(NULLIF(f->'object_storage_requirements','null'::jsonb),'{}'::jsonb)=p.object_storage_requirements,false) THEN
   RAISE EXCEPTION 'binding release policy changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 PERFORM set_config('gregale.binding_release_fences',fences::text,true);
 RETURN true;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Clearing requirements requires an explicit reviewed policy update first.
-- Serialize the guard with writers before checking; later DDL locks are too late.
LOCK TABLE app_binding_release_policies IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app_binding_release_policies WHERE object_storage_requirements<>'{}'::jsonb) THEN
  RAISE EXCEPTION 'clear object-storage release requirements before rollback' USING ERRCODE='23514',CONSTRAINT='object_storage_release_rollback';
 END IF;
END $$;
-- Remove the dependent function body before removing its column.
CREATE OR REPLACE FUNCTION authorize_binding_release_traffic(fences jsonb) RETURNS boolean LANGUAGE plpgsql AS $$
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
  IF p.mode='enforce' AND NOT coalesce((f->>'policy_revision')::bigint=p.revision AND (f->>'max_age_seconds')::numeric>0 AND (f->>'max_age_seconds')::numeric<=p.max_age_seconds AND NOT (f->>'allow_unsupported')::boolean AND (NOT p.require_application_ack OR (f->>'require_application_ack')::boolean),false) THEN
   RAISE EXCEPTION 'binding release policy changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 PERFORM set_config('gregale.binding_release_fences',fences::text,true);
 RETURN true;
END $$;
ALTER TABLE app_binding_release_policies DROP COLUMN object_storage_requirements;
DROP FUNCTION valid_object_storage_release_requirements(jsonb);
-- +goose StatementEnd
