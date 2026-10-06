-- adr: 606
-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (
 binding_verification IS NULL OR COALESCE((
  jsonb_typeof(binding_verification) = 'object'
  AND binding_verification ?& ARRAY['type', 'binding', 'revision']
  AND binding_verification->>'type' IN ('service', 'postgres', 'object_storage', 'outbound')
  AND binding_verification->>'binding' = command[2]
  AND binding_verification->>'revision' ~ '^[a-f0-9]{64}$'
  AND (binding_verification->>'target_deployment_id' IS NULL OR (binding_verification->>'type'='service' AND binding_verification->>'target_deployment_id'=command[3] AND binding_verification->>'target_deployment_id' ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'))
  AND cardinality(command) = CASE WHEN binding_verification->>'type'='outbound' OR binding_verification->>'target_deployment_id' IS NOT NULL THEN 3 ELSE 2 END AND NOT command_shell AND kind = 'manual'
  AND command[1] = CASE binding_verification->>'type'
   WHEN 'service' THEN '__gregale_service_binding_probe_v1__'
   WHEN 'postgres' THEN '__gregale_postgres_binding_probe_v1__'
   WHEN 'object_storage' THEN '__gregale_object_storage_binding_probe_v1__'
   ELSE '__gregale_outbound_binding_probe_v1__' END
 ), false)
);
CREATE OR REPLACE FUNCTION authorize_binding_release_graph(candidate uuid, previous uuid, fences jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE rs project_release_sets%ROWTYPE; f jsonb; member_count integer;
BEGIN
 IF jsonb_typeof(fences) IS DISTINCT FROM 'array' THEN
  RAISE EXCEPTION 'candidate evidence must be an array' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 SELECT * INTO rs FROM project_release_sets WHERE id=candidate AND NOT active;
 IF rs.id IS NULL THEN
  RAISE EXCEPTION 'candidate graph changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 IF (SELECT id FROM project_release_sets WHERE project_id=rs.project_id AND environment_slug=rs.environment_slug AND active) IS DISTINCT FROM previous THEN
  RAISE EXCEPTION 'active graph changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 SELECT count(*) INTO member_count FROM project_release_members WHERE release_id=candidate;
 IF member_count=0 OR member_count<>jsonb_array_length(fences)
   OR member_count<>(SELECT count(*) FROM apps WHERE project_id=rs.project_id AND account_id=rs.account_id AND status<>'deleted' AND coalesce(preview_of_slug,'')='') THEN
  RAISE EXCEPTION 'candidate graph is incomplete' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR f IN SELECT value FROM jsonb_array_elements(fences) LOOP
  IF f->>'account_id' IS DISTINCT FROM rs.account_id::text OR f->>'scope' IS DISTINCT FROM rs.environment_slug OR
    coalesce((SELECT revision FROM app_binding_release_policies WHERE app_id=(f->>'app_id')::uuid AND scope=rs.environment_slug),0) IS DISTINCT FROM (f->>'policy_revision')::bigint OR
    NOT EXISTS(SELECT 1 FROM project_release_members rm JOIN apps a ON a.id=rm.app_id
      WHERE rm.release_id=candidate AND rm.app_id=(f->>'app_id')::uuid AND rm.deployment_id=(f->>'deployment_id')::uuid
       AND a.project_id=rs.project_id AND a.account_id=rs.account_id AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')='') OR
    coalesce((f->>'allow_unsupported')::boolean,true) THEN
   RAISE EXCEPTION 'candidate evidence changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 IF (SELECT count(DISTINCT value->>'app_id') FROM jsonb_array_elements(fences))<>member_count THEN
  RAISE EXCEPTION 'duplicate candidate evidence' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 PERFORM authorize_binding_release_traffic(fences);
 PERFORM set_config('gregale.binding_release_graph',jsonb_build_object('candidate',candidate,'previous',previous,'project',rs.project_id,'environment',rs.environment_slug)::text,true);
 RETURN true;
END $$;

CREATE OR REPLACE FUNCTION enforce_binding_release_graph() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid; grant_data jsonb; candidate uuid; fences jsonb; protected boolean:=false;
BEGIN
 IF (TG_OP='UPDATE' AND NEW.active=OLD.active) OR (TG_OP='INSERT' AND NOT NEW.active) THEN RETURN NEW; END IF;
 FOR app IN SELECT id FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' ORDER BY id LOOP
  PERFORM 1 FROM app_binding_promotion_revisions WHERE app_id=app FOR UPDATE;
  IF EXISTS(SELECT 1 FROM app_binding_release_policies WHERE app_id=app AND scope=NEW.environment_slug AND mode='enforce') THEN protected:=true; END IF;
 END LOOP;
 grant_data:=NULLIF(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF grant_data IS NULL THEN
  IF protected THEN RAISE EXCEPTION 'project release switching requires a binding release gate' USING ERRCODE='23514',CONSTRAINT='binding_release_required'; END IF;
  RETURN NEW;
 END IF;
 candidate:=(grant_data->>'candidate')::uuid;
 IF grant_data->>'project'<>NEW.project_id::text OR grant_data->>'environment'<>NEW.environment_slug
   OR (NEW.active AND NEW.id<>candidate) OR (NOT NEW.active AND NEW.id IS DISTINCT FROM (grant_data->>'previous')::uuid) THEN
  RAISE EXCEPTION 'graph grant does not match transition' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 fences:=NULLIF(current_setting('gregale.binding_release_fences',true),'')::jsonb;
 -- Recheck ALL deadlines after every lock wait, at each pointer write.
 -- The candidate's immutable members must still match every exact fence.
 IF fences IS NULL OR jsonb_array_length(fences)<>(SELECT count(*) FROM project_release_members WHERE release_id=candidate)
   OR EXISTS(SELECT 1 FROM project_release_members rm WHERE rm.release_id=candidate AND NOT EXISTS(
     SELECT 1 FROM jsonb_array_elements(fences) f WHERE f->>'app_id'=rm.app_id::text AND f->>'deployment_id'=rm.deployment_id::text)) THEN
  RAISE EXCEPTION 'graph evidence changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 PERFORM authorize_binding_release_traffic(fences);
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE app_tasks SET binding_verification=NULL WHERE binding_verification->>'target_deployment_id' IS NOT NULL;
ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (
 binding_verification IS NULL OR COALESCE((
  jsonb_typeof(binding_verification) = 'object'
  AND binding_verification ?& ARRAY['type', 'binding', 'revision']
  AND binding_verification->>'type' IN ('service', 'postgres', 'object_storage', 'outbound')
  AND binding_verification->>'binding' = command[2]
  AND binding_verification->>'revision' ~ '^[a-f0-9]{64}$'
  AND cardinality(command) = CASE WHEN binding_verification->>'type'='outbound' THEN 3 ELSE 2 END AND NOT command_shell AND kind = 'manual'
  AND command[1] = CASE binding_verification->>'type'
   WHEN 'service' THEN '__gregale_service_binding_probe_v1__'
   WHEN 'postgres' THEN '__gregale_postgres_binding_probe_v1__'
   WHEN 'object_storage' THEN '__gregale_object_storage_binding_probe_v1__'
   ELSE '__gregale_outbound_binding_probe_v1__' END
 ), false)
);
CREATE OR REPLACE FUNCTION enforce_binding_release_graph() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid;
BEGIN
 IF (TG_OP='UPDATE' AND NEW.active=OLD.active) OR (TG_OP='INSERT' AND NOT NEW.active) THEN RETURN NEW; END IF;
 FOR app IN SELECT id FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' ORDER BY id LOOP
  PERFORM 1 FROM app_binding_promotion_revisions WHERE app_id=app FOR UPDATE;
  IF EXISTS(SELECT 1 FROM app_binding_release_policies WHERE app_id=app AND scope=NEW.environment_slug AND mode='enforce') THEN
   RAISE EXCEPTION 'project release switching requires a binding release gate' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
DROP FUNCTION IF EXISTS authorize_binding_release_graph(uuid,uuid,jsonb);
-- +goose StatementEnd
