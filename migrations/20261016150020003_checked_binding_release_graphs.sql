-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authorize_binding_release_graph(target_project uuid, target_environment text, next_release uuid, prior_release text, members jsonb, fences jsonb) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE a apps%ROWTYPE; f jsonb; ready_count integer; latest text;
BEGIN
 IF jsonb_typeof(members) <> 'object' OR (SELECT count(*) FROM jsonb_each(members)) = 0 THEN
  RAISE EXCEPTION 'invalid checked graph' USING ERRCODE='23514', CONSTRAINT='binding_release_changed';
 END IF;
 -- publishProjectReleaseSetTx already holds the project and app locks.
 SELECT id::text INTO latest FROM project_release_sets WHERE project_id=target_project AND environment_slug=target_environment AND active;
 IF coalesce(latest,'') <> prior_release THEN RAISE EXCEPTION 'active graph changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed'; END IF;
 PERFORM authorize_binding_release_traffic(fences);
 IF (SELECT count(*) FROM apps WHERE project_id=target_project AND status<>'deleted' AND coalesce(preview_of_slug,'')='') <> (SELECT count(*) FROM jsonb_each(members)) THEN
  RAISE EXCEPTION 'graph membership changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR a IN SELECT * FROM apps WHERE project_id=target_project AND status<>'deleted' AND coalesce(preview_of_slug,'')='' ORDER BY id LOOP
  SELECT value INTO f FROM jsonb_array_elements(fences) WHERE value->>'app_id'=a.id::text;
  IF f IS NULL OR f->>'deployment_id' IS DISTINCT FROM members->>a.id::text OR f->>'account_id' IS DISTINCT FROM a.account_id::text OR f->>'scope' IS DISTINCT FROM target_environment OR
    NOT EXISTS (SELECT 1 FROM app_binding_release_policies p WHERE p.app_id=a.id AND p.scope=target_environment AND p.mode='enforce') THEN
   RAISE EXCEPTION 'graph member lacks enforced evidence' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
  END IF;
  IF coalesce(a.manifest->>'execution_mode','')='service' THEN
   SELECT count(*) INTO ready_count FROM (SELECT id FROM instances WHERE app_id=a.id AND deployment_id=(members->>a.id::text)::uuid AND mode='service' AND state='running' FOR SHARE) ready;
   IF ready_count < greatest(1,coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) THEN
    RAISE EXCEPTION 'graph service recipient is not ready' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
   END IF;
  END IF;
 END LOOP;
 PERFORM set_config('gregale.binding_release_graph',jsonb_build_object('project_id',target_project,'environment',target_environment,'new_id',next_release,'old_id',prior_release,'members',members,'fences',fences)::text,true);
 RETURN true;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_binding_release_graph() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE grant_value jsonb; f jsonb;
BEGIN
 IF TG_OP='UPDATE' AND NEW.active=OLD.active THEN RETURN NEW; END IF;
 IF TG_OP='INSERT' AND NOT NEW.active THEN RETURN NEW; END IF;
 PERFORM 1 FROM app_binding_promotion_revisions r JOIN apps a ON a.id=r.app_id
 WHERE a.project_id=NEW.project_id ORDER BY r.app_id FOR UPDATE OF r;
 IF EXISTS (SELECT 1 FROM app_binding_release_policies p JOIN apps a ON a.id=p.app_id
 WHERE a.project_id=NEW.project_id AND p.scope=NEW.environment_slug AND p.mode='enforce') THEN
  grant_value := nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
  IF grant_value IS NULL OR grant_value->>'project_id' IS DISTINCT FROM NEW.project_id::text OR grant_value->>'environment' IS DISTINCT FROM NEW.environment_slug OR
   (TG_OP='INSERT' AND (NOT NEW.active OR grant_value->>'new_id' IS DISTINCT FROM NEW.id::text)) OR
   (TG_OP='UPDATE' AND (NEW.active OR grant_value->>'old_id' IS DISTINCT FROM NEW.id::text)) THEN
   RAISE EXCEPTION 'project graph requires checked binding evidence' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
  END IF;
  FOR f IN SELECT value FROM jsonb_array_elements(grant_value->'fences') LOOP
   IF nullif(f->>'valid_until','')::timestamptz <= clock_timestamp() THEN
    RAISE EXCEPTION 'graph evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
   END IF;
  END LOOP;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_checked_binding_release_graph() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE grant_value jsonb; actual_members jsonb; f jsonb;
BEGIN
 grant_value := nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF grant_value IS NULL OR NEW.id::text IS DISTINCT FROM grant_value->>'new_id' THEN RETURN NEW; END IF;
 SELECT coalesce(jsonb_object_agg(app_id::text,deployment_id::text),'{}'::jsonb) INTO actual_members FROM project_release_members WHERE release_id=NEW.id;
 IF actual_members IS DISTINCT FROM grant_value->'members' THEN
  RAISE EXCEPTION 'checked graph members changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR f IN SELECT value FROM jsonb_array_elements(grant_value->'fences') LOOP
  IF nullif(f->>'valid_until','')::timestamptz <= clock_timestamp() THEN
   RAISE EXCEPTION 'graph evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='project_release_sets'::regclass AND tgname='checked_binding_release_graph_members') THEN
  CREATE CONSTRAINT TRIGGER checked_binding_release_graph_members AFTER INSERT ON project_release_sets DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_checked_binding_release_graph();
 END IF;
END $replay$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER checked_binding_release_graph_members ON project_release_sets;
DROP FUNCTION validate_checked_binding_release_graph();
DROP FUNCTION authorize_binding_release_graph(uuid,text,uuid,text,jsonb,jsonb);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_binding_release_graph() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.active=OLD.active THEN RETURN NEW; END IF;
 IF TG_OP='INSERT' AND NOT NEW.active THEN RETURN NEW; END IF;
 PERFORM 1 FROM app_binding_promotion_revisions r JOIN apps a ON a.id=r.app_id WHERE a.project_id=NEW.project_id ORDER BY r.app_id FOR UPDATE OF r;
 IF EXISTS (SELECT 1 FROM app_binding_release_policies p JOIN apps a ON a.id=p.app_id WHERE a.project_id=NEW.project_id AND p.scope=NEW.environment_slug AND p.mode='enforce') THEN
  RAISE EXCEPTION 'project graph requires checked binding evidence' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
