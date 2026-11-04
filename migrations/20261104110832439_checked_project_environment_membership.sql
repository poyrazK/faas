-- filename: 20261104110832439_checked_project_environment_membership.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep checked graph authorization and deferred deactivation aligned with
-- environment admission, including services configured only in another stage.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_release_environment_apps(target_project uuid, target_environment text)
RETURNS SETOF apps LANGUAGE sql STABLE AS $members$
 SELECT a.* FROM apps a
 WHERE a.project_id=target_project AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')=''
 AND CASE WHEN target_environment IN ('', 'default', 'production') THEN NOT (
   coalesce(a.manifest->>'project_source_sha256', '')=''
   AND EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug<>'production')
   AND NOT EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug='production')
   AND NOT EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope IN ('', 'default', 'production'))
 ) ELSE (
   EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug=target_environment)
   OR EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=target_environment)
 ) END
$members$;
-- +goose StatementEnd

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
 IF (SELECT count(*) FROM project_release_environment_apps(target_project,target_environment)) <> (SELECT count(*) FROM jsonb_each(members)) THEN
  RAISE EXCEPTION 'graph membership changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR a IN SELECT * FROM project_release_environment_apps(target_project,target_environment) ORDER BY id LOOP
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
CREATE OR REPLACE FUNCTION validate_checked_project_deactivation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g jsonb; receipt jsonb; selected jsonb; actual jsonb; a apps%ROWTYPE; expected text; f jsonb;
BEGIN
 g:=nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF g->>'action' IS DISTINCT FROM 'deactivate' THEN
  IF OLD.active AND NOT NEW.active AND g->>'old_id'=NEW.id::text AND NOT EXISTS (SELECT 1 FROM project_release_sets WHERE id=(g->>'new_id')::uuid AND active AND project_id=NEW.project_id AND environment_slug=NEW.environment_slug) THEN
   RAISE EXCEPTION 'forward grant requires replacement graph' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  RETURN NEW;
 END IF;
 IF g->>'old_id' IS DISTINCT FROM NEW.id::text THEN RETURN NEW; END IF;
 SELECT deactivation INTO receipt FROM project_release_sets WHERE id=NEW.id AND NOT active;
 IF receipt IS NULL OR receipt->>'id' IS DISTINCT FROM g->>'receipt_id' OR EXISTS (SELECT 1 FROM project_release_sets WHERE project_id=NEW.project_id AND environment_slug=NEW.environment_slug AND active) THEN
  RAISE EXCEPTION 'deactivation lacks exact receipt' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 SELECT jsonb_object_agg(value->>'app_id',value->>'deployment_id') INTO selected FROM jsonb_array_elements(receipt->'fallback_members');
 IF selected IS DISTINCT FROM g->'members' OR jsonb_array_length(receipt->'fallback_members')<>(SELECT count(*) FROM jsonb_each(g->'members')) THEN
  RAISE EXCEPTION 'deactivation recipients changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 IF (SELECT count(*) FROM project_release_environment_apps(NEW.project_id,NEW.environment_slug))<>(SELECT count(*) FROM jsonb_each(selected)) THEN
  RAISE EXCEPTION 'fallback membership changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR a IN SELECT * FROM project_release_environment_apps(NEW.project_id,NEW.environment_slug) ORDER BY id LOOP
  expected:=selected->>a.id::text;
  SELECT jsonb_object_agg(id::text,traffic_percent) INTO actual FROM deployments WHERE app_id=a.id AND scope=NEW.environment_slug AND status='live' AND traffic_percent>0;
  IF expected IS NULL OR actual IS DISTINCT FROM jsonb_build_object(expected,100) OR NOT EXISTS (SELECT 1 FROM deployments WHERE id=expected::uuid AND app_id=a.id AND scope=NEW.environment_slug AND status='live' AND NOT environment_workload_held AND coalesce(rootfs_key,'')<>'' AND coalesce(image_digest,'')<>'') THEN
   RAISE EXCEPTION 'fallback routing or eligibility changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 FOR f IN SELECT value FROM jsonb_array_elements(g->'fences') LOOP
  IF nullif(f->>'valid_until','')::timestamptz<=clock_timestamp() THEN
   RAISE EXCEPTION 'deactivation evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

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
CREATE OR REPLACE FUNCTION validate_checked_project_deactivation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g jsonb; receipt jsonb; selected jsonb; actual jsonb; a apps%ROWTYPE; expected text; f jsonb;
BEGIN
 g:=nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF g->>'action' IS DISTINCT FROM 'deactivate' THEN
  IF OLD.active AND NOT NEW.active AND g->>'old_id'=NEW.id::text AND NOT EXISTS (SELECT 1 FROM project_release_sets WHERE id=(g->>'new_id')::uuid AND active AND project_id=NEW.project_id AND environment_slug=NEW.environment_slug) THEN
   RAISE EXCEPTION 'forward grant requires replacement graph' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  RETURN NEW;
 END IF;
 IF g->>'old_id' IS DISTINCT FROM NEW.id::text THEN RETURN NEW; END IF;
 SELECT deactivation INTO receipt FROM project_release_sets WHERE id=NEW.id AND NOT active;
 IF receipt IS NULL OR receipt->>'id' IS DISTINCT FROM g->>'receipt_id' OR EXISTS (SELECT 1 FROM project_release_sets WHERE project_id=NEW.project_id AND environment_slug=NEW.environment_slug AND active) THEN
  RAISE EXCEPTION 'deactivation lacks exact receipt' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 SELECT jsonb_object_agg(value->>'app_id',value->>'deployment_id') INTO selected FROM jsonb_array_elements(receipt->'fallback_members');
 IF selected IS DISTINCT FROM g->'members' OR jsonb_array_length(receipt->'fallback_members')<>(SELECT count(*) FROM jsonb_each(g->'members')) THEN
  RAISE EXCEPTION 'deactivation recipients changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 IF (SELECT count(*) FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' AND coalesce(preview_of_slug,'')='')<>(SELECT count(*) FROM jsonb_each(selected)) THEN
  RAISE EXCEPTION 'fallback membership changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR a IN SELECT * FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' AND coalesce(preview_of_slug,'')='' ORDER BY id LOOP
  expected:=selected->>a.id::text;
  SELECT jsonb_object_agg(id::text,traffic_percent) INTO actual FROM deployments WHERE app_id=a.id AND scope=NEW.environment_slug AND status='live' AND traffic_percent>0;
  IF expected IS NULL OR actual IS DISTINCT FROM jsonb_build_object(expected,100) OR NOT EXISTS (SELECT 1 FROM deployments WHERE id=expected::uuid AND app_id=a.id AND scope=NEW.environment_slug AND status='live' AND NOT environment_workload_held AND coalesce(rootfs_key,'')<>'' AND coalesce(image_digest,'')<>'') THEN
   RAISE EXCEPTION 'fallback routing or eligibility changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 FOR f IN SELECT value FROM jsonb_array_elements(g->'fences') LOOP
  IF nullif(f->>'valid_until','')::timestamptz<=clock_timestamp() THEN
   RAISE EXCEPTION 'deactivation evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP FUNCTION project_release_environment_apps(uuid,text);
