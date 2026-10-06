-- GitOps audit: retain retired bindings and physical external field ownership.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE environment_git_sources ADD COLUMN IF NOT EXISTS detached boolean NOT NULL DEFAULT false;
ALTER TABLE environment_git_sources DROP CONSTRAINT IF EXISTS environment_git_sources_environment_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS environment_git_sources_active_environment ON environment_git_sources(environment_id) WHERE NOT detached;
ALTER TABLE environment_git_sources DROP CONSTRAINT IF EXISTS environment_git_sources_detached_suspended;
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_detached_suspended CHECK (NOT detached OR (suspended AND mode='report'));
CREATE OR REPLACE VIEW active_environment_git_sources AS SELECT * FROM environment_git_sources WHERE NOT detached;
CREATE TABLE IF NOT EXISTS environment_external_field_owners (
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 resource text NOT NULL CHECK(resource='environment' OR resource ~ '^app/[a-f0-9-]{36}$'),
 field_path text NOT NULL CHECK(field_path='source' OR field_path ~ '^(variables|configuration)/[^/#]+$'),
 manager_id text NOT NULL CHECK(manager_id='terraform'),
 PRIMARY KEY(environment_id,resource,field_path)
);
ALTER TABLE environment_gitops_events DROP CONSTRAINT IF EXISTS environment_gitops_events_kind_check;
ALTER TABLE environment_gitops_events ADD CONSTRAINT environment_gitops_events_kind_check CHECK(kind IN ('adopt','control','override_created','override_removed','detached','rebound'));
CREATE INDEX IF NOT EXISTS environment_gitops_runs_completed_history ON environment_gitops_runs(source_id,completed_at DESC,id DESC) WHERE completed_at IS NOT NULL;
CREATE OR REPLACE FUNCTION public.environment_gitops_guard_app_presence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE src environment_git_sources%ROWTYPE;
BEGIN
    IF pg_trigger_depth() > 1 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.status = 'deleted') = (OLD.status = 'deleted') THEN RETURN NEW; END IF;
    FOR src IN SELECT s.* FROM active_environment_git_sources s
        JOIN environment_gitops_resources r ON r.source_id = s.id
        WHERE r.app_id = OLD.id ORDER BY s.id FOR UPDATE OF s
    LOOP
        IF (TG_OP = 'DELETE' OR NEW.status = 'deleted') AND src.mode = 'enforce' AND EXISTS (
            SELECT 1 FROM environment_managed_fields f JOIN environment_gitops_resources r
            ON r.source_id = f.source_id AND r.logical_name = f.resource
            WHERE f.source_id = src.id AND r.app_id = OLD.id AND f.field_path = 'presence'
              AND NOT EXISTS (SELECT 1 FROM environment_management_overrides o
                  WHERE o.environment_id = f.environment_id AND o.resource = f.resource AND o.field_path = 'presence'
                    AND o.expires_at > clock_timestamp())) THEN
            RAISE EXCEPTION 'workload membership is managed by the environment Git source'
                USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
        END IF;
        UPDATE environment_git_sources SET intent_version = intent_version + 1, updated_at = now() WHERE id = src.id;
        IF src.generation > 0 THEN
            INSERT INTO environment_gitops_jobs(source_id, desired_generation, next_attempt_at)
            VALUES (src.id, src.generation, now()) ON CONFLICT (source_id) DO UPDATE
            SET next_attempt_at = least(environment_gitops_jobs.next_attempt_at, excluded.next_attempt_at);
        END IF;
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;

CREATE OR REPLACE FUNCTION public.environment_gitops_guard_config_history() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF pg_trigger_depth() <= 1 AND EXISTS (
        SELECT 1 FROM active_environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
        WHERE s.project_id = OLD.project_id AND s.account_id = OLD.account_id AND e.slug = OLD.environment_slug) THEN
        RAISE EXCEPTION 'managed configuration history is append-only'
            USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;

CREATE OR REPLACE FUNCTION public.environment_gitops_guard_identity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF (to_jsonb(NEW) - ARRAY['value', 'updated_at', 'only_allow_declared_routes', 'declared_routes', 'rules'])
       IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['value', 'updated_at', 'only_allow_declared_routes', 'declared_routes', 'rules'])
       AND EXISTS (SELECT 1 FROM environment_gitops_resources r JOIN active_environment_git_sources s ON s.id = r.source_id
           JOIN project_environments e ON e.id = s.environment_id
           WHERE r.app_id = OLD.app_id AND s.account_id = OLD.account_id
           AND e.slug = coalesce(to_jsonb(OLD)->>'scope', to_jsonb(OLD)->>'environment_slug')) THEN
        RAISE EXCEPTION 'environment resource identity cannot be moved'
            USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
    END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION public.environment_gitops_guard_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    row_value jsonb;
    src environment_git_sources%ROWTYPE;
    resource_name text;
    paths text[];
    prior_config jsonb;
    controller boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN row_value := to_jsonb(OLD); ELSE row_value := to_jsonb(NEW); END IF;
    IF TG_TABLE_NAME = 'project_environment_config_versions' THEN
        SELECT s.* INTO src FROM active_environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
        WHERE s.account_id = (row_value->>'account_id')::uuid AND s.project_id = (row_value->>'project_id')::uuid
          AND e.slug = row_value->>'environment_slug' FOR UPDATE OF s;
        resource_name := 'environment';
        SELECT config_json INTO prior_config FROM project_environment_config_versions
        WHERE project_id = (row_value->>'project_id')::uuid AND environment_slug = row_value->>'environment_slug'
        ORDER BY version DESC LIMIT 1;
        SELECT array_agg('configuration/' || k) INTO paths FROM (
            SELECT key AS k FROM jsonb_each(coalesce(prior_config, '{}'))
            UNION SELECT key FROM jsonb_each(coalesce(row_value->'config_json', '{}'))
        ) keys WHERE (prior_config->k) IS DISTINCT FROM (row_value->'config_json'->k);
    ELSE
        SELECT s.* INTO src
        FROM active_environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
        JOIN environment_gitops_resources r ON r.source_id = s.id
        WHERE s.account_id = (row_value->>'account_id')::uuid AND r.app_id = (row_value->>'app_id')::uuid
          AND e.slug = coalesce(row_value->>'scope', row_value->>'environment_slug') FOR UPDATE OF s;
        SELECT logical_name INTO resource_name FROM environment_gitops_resources
        WHERE source_id = src.id AND app_id = (row_value->>'app_id')::uuid;
        IF TG_TABLE_NAME = 'app_envs' THEN paths := ARRAY['variables/' || (row_value->>'key')];
        ELSIF TG_TABLE_NAME = 'project_environment_route_policies' THEN paths := ARRAY['routes'];
        ELSE paths := ARRAY['policies']; END IF;
    END IF;
    IF src.id IS NOT NULL THEN
        controller := EXISTS (SELECT 1 FROM environment_gitops_jobs j
            WHERE j.source_id = src.id AND j.desired_generation = src.generation AND j.claimed_generation = src.generation
              AND j.lease_until > clock_timestamp() AND j.lease_token <> ''
              AND j.lease_token = current_setting('gregale.gitops_lease', true)
              AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
        IF src.mode = 'enforce' AND NOT controller AND EXISTS (
            SELECT 1 FROM environment_managed_fields f
            WHERE f.environment_id = src.environment_id AND f.resource = resource_name AND f.field_path = ANY(paths)
              AND f.source_id = src.id AND NOT EXISTS (
                  SELECT 1 FROM environment_management_overrides o
                  WHERE o.environment_id = f.environment_id AND o.resource = f.resource AND o.field_path = f.field_path
                    AND o.expires_at > clock_timestamp())) THEN
            RAISE EXCEPTION 'setting is managed by the environment Git source'
                USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
        END IF;
        UPDATE environment_git_sources SET intent_version = intent_version + 1, updated_at = now() WHERE id = src.id;
        IF src.generation > 0 THEN
            INSERT INTO environment_gitops_jobs(source_id, desired_generation, next_attempt_at)
            VALUES (src.id, src.generation, now()) ON CONFLICT (source_id) DO UPDATE
            SET next_attempt_at = least(environment_gitops_jobs.next_attempt_at, excluded.next_attempt_at);
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;

CREATE OR REPLACE FUNCTION public.environment_gitops_queue_recovery_authorized(target_binding uuid) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
DECLARE src environment_git_sources%ROWTYPE;
BEGIN
 -- Legacy SQL writers may acquire the binding first. Serialize authority on
 -- the source too; a deadlock aborts the whole transaction rather than granting
 -- a stale approved generation permission to release retained work.
 SELECT s.* INTO src FROM active_environment_git_sources s JOIN queue_bindings b ON b.environment_id=s.environment_id
  JOIN apps a ON a.id=b.app_id AND a.account_id=s.account_id AND a.project_id=s.project_id
  WHERE b.id=target_binding AND b.account_id=s.account_id FOR UPDATE OF s;
 IF src.id IS NULL OR src.mode<>'enforce' OR src.suspended THEN RETURN false; END IF;
 RETURN environment_gitops_queue_recovery_declared(src.id,target_binding) AND EXISTS(
  SELECT 1 FROM environment_gitops_jobs j
   JOIN environment_gitops_queue_bindings q ON q.source_id=j.source_id AND q.binding_id=target_binding
   JOIN environment_managed_fields f ON f.source_id=q.source_id AND f.resource=q.resource AND f.field_path=q.field_path
  WHERE j.source_id=src.id AND j.desired_generation=src.generation AND j.claimed_generation=src.generation
   AND j.lease_until>clock_timestamp() AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=src.environment_id
    AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp()));
END;
$$;

CREATE OR REPLACE FUNCTION public.environment_gitops_queue_recovery_declared(target_source uuid, target_binding uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(SELECT 1 FROM active_environment_git_sources s
  JOIN environment_desired_revisions v ON v.id=s.approved_revision_id AND v.source_id=s.id
  JOIN environment_gitops_resources r ON r.source_id=s.id
  JOIN queue_bindings b ON b.id=target_binding AND b.app_id=r.app_id AND b.account_id=s.account_id AND b.environment_id=s.environment_id
  JOIN project_environments e ON e.id=s.environment_id AND e.account_id=s.account_id AND e.project_id=s.project_id
  WHERE s.id=target_source AND r.logical_name LIKE 'workload/%'
   AND v.definition->'workloads'->substr(r.logical_name,10)->'queue_recoveries'->>b.name=b.id::text
   AND v.definition->'workloads'->substr(r.logical_name,10)->'queue_bindings' ? b.name);
$$;

CREATE OR REPLACE FUNCTION public.environment_runtime_receipt_required(target_app uuid, target_scope text) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT environment_scoped_secret_refs(target_app,target_scope)<>'{}'::jsonb
  OR cardinality(environment_scoped_secret_suppressions(target_app,target_scope))>0 OR EXISTS (
  SELECT 1 FROM active_environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
  JOIN environment_gitops_resources r ON r.source_id=s.id WHERE r.app_id=target_app AND e.slug=target_scope
  AND (EXISTS (SELECT 1 FROM environment_managed_fields f WHERE f.source_id=s.id AND f.resource=r.logical_name
   AND (f.field_path LIKE 'variables/%' OR f.field_path LIKE 'secret_refs/%'))
   OR EXISTS (SELECT 1 FROM environment_gitops_runtime_effects x WHERE x.source_id=s.id AND x.app_id=r.app_id AND x.completed_at IS NULL)));
$$;

CREATE OR REPLACE FUNCTION public.environment_workload_qualification_inputs_current(request_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_qualification_requests target
  JOIN environment_workload_graphs g ON g.id=target.graph_id JOIN active_environment_git_sources s ON s.id=g.source_id
  JOIN project_environments e ON e.id=g.environment_id JOIN accounts c ON c.id=s.account_id
  WHERE target.id=request_id AND g.phase='prepared' AND s.mode='enforce' AND NOT s.suspended
   AND c.status='active' AND c.abuse_hold_at IS NULL
   AND g.generation=s.generation AND g.intent_version=s.intent_version AND g.revision_id=s.approved_revision_id
   AND g.environment_id=s.environment_id AND e.account_id=s.account_id AND e.project_id=s.project_id
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m LEFT JOIN apps a ON a.id=(m->>'app_id')::uuid
    WHERE a.id IS NULL OR a.status NOT IN ('active','evicted_cold') OR a.account_id<>s.account_id OR a.project_id<>s.project_id
     OR m->'retained_deployments' IS DISTINCT FROM (SELECT coalesce(jsonb_agg(d.id::text ORDER BY d.id),'[]'::jsonb)
      FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'))
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m
    LEFT JOIN environment_workload_qualification_requests q ON q.graph_id=g.id AND q.resource=m->>'resource'
     AND q.app_id=(m->>'app_id')::uuid AND q.deployment_id=(m->>'candidate_deployment_id')::uuid
    LEFT JOIN deployments d ON d.id=q.deployment_id LEFT JOIN apps a ON a.id=q.app_id
    WHERE m ? 'candidate_deployment_id' AND (q.id IS NULL OR d.status IS DISTINCT FROM 'snapshotting' OR coalesce(d.rootfs_bytes,0)<=0
     OR q.artifact IS DISTINCT FROM environment_workload_artifact(d) OR q.frozen_inputs IS DISTINCT FROM d.environment_workload_runtime
     OR d.scope IS DISTINCT FROM e.slug OR d.app_id IS DISTINCT FROM a.id
     OR jsonb_strip_nulls(q.frozen_inputs->'baseline') IS DISTINCT FROM jsonb_strip_nulls(a.manifest)
     OR q.frozen_inputs->>'start_command' IS DISTINCT FROM coalesce(a.start_command,'') OR q.frozen_inputs->>'app_type' IS DISTINCT FROM a.type
     OR q.frozen_inputs->>'runtime_base' IS DISTINCT FROM coalesce(a.runtime,'') OR q.frozen_inputs->>'workload_class' IS DISTINCT FROM a.workload_class
     OR q.frozen_inputs ? 'deployment_inputs' AND EXISTS(SELECT 1 FROM deployments original
      WHERE original.app_id=a.id AND original.scope=e.slug AND original.status='live'
       AND environment_workload_deployment_inputs(original) IS DISTINCT FROM q.frozen_inputs->'deployment_inputs'))));
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_gitops_queue_identity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.source_id IS DISTINCT FROM OLD.source_id OR NEW.resource IS DISTINCT FROM OLD.resource
  OR NEW.field_path IS DISTINCT FROM OLD.field_path OR (OLD.binding_id IS NOT NULL AND NEW.binding_id IS NOT NULL AND NEW.binding_id IS DISTINCT FROM OLD.binding_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_queue_identity',MESSAGE='Git queue identity requires reviewed recovery';
 END IF;
 IF NEW.binding_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM queue_bindings b JOIN active_environment_git_sources s ON s.id=NEW.source_id
   JOIN environment_gitops_resources r ON r.source_id=s.id AND r.logical_name=NEW.resource
  WHERE b.id=NEW.binding_id AND b.app_id=r.app_id AND b.account_id=s.account_id
   AND b.environment_id=s.environment_id AND NEW.field_path='queue_bindings/'||b.name
   AND (b.retired_at IS NULL OR environment_gitops_queue_recovery_declared(s.id,b.id)) FOR SHARE OF b) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_queue_identity',MESSAGE='Git queue ownership requires its original scoped binding and reviewed recovery';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_gitops_queue_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
 binding queue_bindings%ROWTYPE;
 src environment_git_sources%ROWTYPE;
 resource_name text;
 paths text[];
 controller boolean;
BEGIN
 IF TG_TABLE_NAME='queue_bindings' THEN
  IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at'-'created_at')=(to_jsonb(OLD)-'updated_at'-'created_at') THEN RETURN NEW; END IF;
  IF TG_OP='DELETE' THEN binding:=OLD; ELSE binding:=NEW; END IF;
  paths:=ARRAY['queue_bindings/'||binding.name];
  IF TG_OP='UPDATE' THEN paths:=paths||ARRAY['queue_bindings/'||OLD.name]; END IF;
 ELSE
  IF TG_OP='UPDATE' AND OLD.queue_binding_id IS NOT NULL AND NEW.id IS DISTINCT FROM OLD.id THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='queue_consumer_binding_identity',MESSAGE='private consumer UUID is immutable';
  END IF;
  IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at'-'created_at')=(to_jsonb(OLD)-'updated_at'-'created_at') THEN RETURN NEW; END IF;
  IF TG_OP='DELETE' THEN
   SELECT b.* INTO binding FROM queue_bindings b WHERE b.id=OLD.queue_binding_id;
  ELSE
   SELECT b.* INTO binding FROM queue_bindings b WHERE b.id=NEW.queue_binding_id;
  END IF;
  paths:=ARRAY['queue_bindings/'||binding.name];
 END IF;
 IF binding.environment_id IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 SELECT s.* INTO src FROM active_environment_git_sources s JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
  WHERE s.environment_id=binding.environment_id AND s.account_id=binding.account_id AND a.id=binding.app_id FOR UPDATE OF s;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=binding.app_id;
  SELECT paths||coalesce(array_agg(q.field_path),'{}'::text[]) INTO paths FROM environment_gitops_queue_bindings q
   WHERE q.source_id=src.id AND q.binding_id=binding.id;
  controller:=EXISTS (SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id
   AND j.desired_generation=src.generation AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp()
   AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS (
   SELECT 1 FROM environment_managed_fields f WHERE f.environment_id=src.environment_id AND f.resource=resource_name
    AND f.field_path=ANY(paths) AND f.source_id=src.id AND NOT EXISTS (
     SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=f.environment_id AND o.resource=f.resource
      AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
    ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_qualification_runtime_receipt() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; i instances%ROWTYPE;
BEGIN
 SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=i.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=i.id;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM active_environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=i.id FOR UPDATE;
  PERFORM n.id FROM compute_nodes n WHERE n.id=i.node_id FOR SHARE OF n;
  PERFORM c.id FROM accounts c JOIN apps a ON a.account_id=c.id WHERE a.id=i.app_id FOR SHARE OF c;
  -- Terminal cleanup does not require the graph lock. Re-read and lock the
  -- incarnation after authority locks so a concurrent retirement wins.
  SELECT * INTO i FROM instances WHERE id=NEW.instance_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>i.app_id OR q.deployment_id<>i.deployment_id OR i.state<>'running'
  OR i.ram_mb IS DISTINCT FROM (SELECT a.ram_mb FROM apps a WHERE a.id=i.app_id)
  OR NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=i.node_id AND n.active AND n.lifecycle='active')
  OR NEW.wake_id IS DISTINCT FROM i.wake_id OR NEW.scope IS DISTINCT FROM q.frozen_inputs->>'scope'
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR NOT environment_runtime_inputs_fresh(i.app_id,NEW.scope,NEW.boundary_at,NEW.variables,NEW.secret_versions,NEW.all_secrets,NEW.secret_refs,NEW.sidecar_secret_versions) THEN
  RAISE EXCEPTION 'environment runtime evidence requires its current qualification attempt and delivered inputs' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION public.guard_environment_secret_ref_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE row_value record; src environment_git_sources%ROWTYPE;
 resource_name text; controller boolean; stamp timestamptz;
BEGIN
 IF TG_OP='DELETE' THEN row_value:=OLD; ELSE row_value:=NEW; END IF;
 -- Parent cascades have already removed their catalog/app identity. Ordinary
 -- row deletion still passes through ownership enforcement below.
 IF TG_OP='DELETE' AND NOT EXISTS (SELECT 1 FROM apps a JOIN project_environments e ON e.id=row_value.environment_id
  WHERE a.id=row_value.app_id AND a.account_id=row_value.account_id AND a.project_id=row_value.project_id
   AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=row_value.scope) THEN
  -- Removing catalog intent also invalidates caches captured under its UUID.
  -- The surviving app can retain deployments/sealed provider credentials.
  IF EXISTS(SELECT 1 FROM apps WHERE id=row_value.app_id FOR UPDATE) THEN
   stamp:=clock_timestamp();
   INSERT INTO app_runtime_config_scope_changes(app_id,scope,changed_at) VALUES(row_value.app_id,row_value.scope,stamp)
    ON CONFLICT(app_id,scope) DO UPDATE SET changed_at=greatest(app_runtime_config_scope_changes.changed_at,excluded.changed_at);
   UPDATE snapshots p SET stale=true FROM deployments d WHERE p.deployment_id=d.id AND d.app_id=row_value.app_id
    AND d.scope=row_value.scope AND NOT p.stale;
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.project_id IS DISTINCT FROM OLD.project_id
  OR NEW.environment_id IS DISTINCT FROM OLD.environment_id OR NEW.app_id IS DISTINCT FROM OLD.app_id
  OR NEW.scope IS DISTINCT FROM OLD.scope OR NEW.key IS DISTINCT FROM OLD.key) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_identity',MESSAGE='secret reference identity is immutable';
 END IF;
 SELECT s.* INTO src FROM active_environment_git_sources s
 WHERE s.environment_id=row_value.environment_id AND s.account_id=row_value.account_id FOR UPDATE OF s;
 IF TG_OP<>'DELETE' AND NOT EXISTS (SELECT 1 FROM apps a JOIN project_environments e ON e.id=row_value.environment_id
  WHERE a.id=row_value.app_id AND a.account_id=row_value.account_id AND a.project_id=row_value.project_id
   AND a.status<>'deleted' AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=row_value.scope
  FOR SHARE OF a,e) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_scope',MESSAGE='secret reference requires its account-owned project environment';
 END IF;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=row_value.app_id;
  controller:=EXISTS (SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id
   AND j.desired_generation=src.generation AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp()
   AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS (
   SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=resource_name
    AND f.field_path='secret_refs/'||row_value.key AND NOT EXISTS (SELECT 1 FROM environment_management_overrides o
     WHERE o.environment_id=f.environment_id AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END IF;
 -- Publication and every ordinary writer share the app lock and scoped stamp.
 IF EXISTS(SELECT 1 FROM apps WHERE id=row_value.app_id FOR UPDATE) THEN
  stamp:=clock_timestamp();
  INSERT INTO app_runtime_config_scope_changes(app_id,scope,changed_at) VALUES(row_value.app_id,row_value.scope,stamp)
   ON CONFLICT(app_id,scope) DO UPDATE SET changed_at=greatest(app_runtime_config_scope_changes.changed_at,excluded.changed_at);
  UPDATE snapshots p SET stale=true FROM deployments d WHERE p.deployment_id=d.id AND d.app_id=row_value.app_id
   AND d.scope=row_value.scope AND NOT p.stale;
 END IF;
 -- Both kinds of intent share the same app lock. Direct SQL cannot retain
 -- a positive reference and a suppression for the same destination.
 IF TG_OP<>'DELETE' THEN
  IF (TG_TABLE_NAME='app_environment_secret_refs' AND EXISTS(SELECT 1 FROM app_environment_secret_ref_suppressions
    WHERE app_id=row_value.app_id AND environment_id=row_value.environment_id AND key=row_value.key))
   OR (TG_TABLE_NAME='app_environment_secret_ref_suppressions' AND EXISTS(SELECT 1 FROM app_environment_secret_refs
    WHERE app_id=row_value.app_id AND environment_id=row_value.environment_id AND key=row_value.key)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_exclusive',MESSAGE='reference and suppression are mutually exclusive';
  END IF;
  -- Mirrored by api.EnvironmentSecretReferenceSuppressionsMaxPerApp.
  IF TG_TABLE_NAME='app_environment_secret_ref_suppressions' AND TG_OP='INSERT'
   AND NOT EXISTS(SELECT 1 FROM app_environment_secret_ref_suppressions WHERE app_id=row_value.app_id AND environment_id=row_value.environment_id AND key=row_value.key)
   AND (SELECT count(*) FROM app_environment_secret_ref_suppressions WHERE app_id=row_value.app_id)>=1024 THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_suppression_quota',MESSAGE='secret reference suppression limit reached';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE NEW.updated_at:=stamp; RETURN NEW; END IF;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_secret_reference_baseline() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE old_app uuid; new_app uuid; old_scope text; new_scope text; src environment_git_sources%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' AND OLD.status='live' THEN old_app:=OLD.app_id; old_scope:=OLD.scope; END IF;
 IF TG_OP<>'DELETE' AND NEW.status='live' THEN new_app:=NEW.app_id; new_scope:=NEW.scope; END IF;
 IF old_app IS NULL AND new_app IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 FOR src IN SELECT s.* FROM active_environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
  JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
  WHERE (a.id=old_app AND e.slug=old_scope) OR (a.id=new_app AND e.slug=new_scope)
  ORDER BY s.id FOR UPDATE OF s LOOP
  PERFORM 1 FROM apps a WHERE (a.id=old_app OR a.id=new_app) AND a.project_id=src.project_id ORDER BY a.id FOR UPDATE;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_secret_reference_shadow() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE src environment_git_sources%ROWTYPE; resource_name text;
BEGIN
 SELECT s.* INTO src FROM active_environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
 JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
 WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id AND e.slug=NEW.scope FOR UPDATE OF s;
 IF src.id IS NULL OR src.mode<>'enforce' THEN RETURN NEW; END IF;
 SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=NEW.app_id;
 IF EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=resource_name
  AND f.field_path='secret_refs/'||NEW.key AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o
   WHERE o.environment_id=f.environment_id AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp()))
 AND NOT EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
  AND j.claimed_generation=src.generation AND j.lease_token=current_setting('gregale.gitops_lease',true)
  AND j.lease_token<>'' AND j.lease_until>clock_timestamp() AND NOT src.suspended AND src.approved_revision_id IS NOT NULL) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_candidate() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE frozen jsonb; src environment_git_sources%ROWTYPE; allowed boolean;
BEGIN
 frozen:=NEW.environment_workload_runtime;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL THEN
  IF NEW.environment_workload_runtime IS DISTINCT FROM OLD.environment_workload_runtime OR
   (OLD.environment_workload_runtime ? 'source_archive' AND ROW(NEW.source_bytes,NEW.source_url,NEW.log_path,NEW.inferred_profile,NEW.github_source_ref,NEW.github_installation_id)
    IS DISTINCT FROM ROW(OLD.source_bytes,OLD.source_url,OLD.log_path,OLD.inferred_profile,OLD.github_source_ref,OLD.github_installation_id)) OR
   (NEW.build_id IS DISTINCT FROM OLD.build_id AND (OLD.build_id IS NOT NULL OR frozen->'source_archive' IS NULL OR
    NEW.build_id IS DISTINCT FROM (frozen->'source_archive'->>'build_id')::uuid OR NOT EXISTS(
     SELECT 1 FROM builds b WHERE b.id=NEW.build_id AND b.deployment_id=NEW.id AND b.kind='github' AND b.source_bytes=NEW.source_bytes))) OR
   environment_workload_deployment_inputs(NEW) IS DISTINCT FROM environment_workload_deployment_inputs(OLD) OR
   ROW(NEW.app_id,NEW.scope,NEW.kind,NEW.image_digest,NEW.commit_sha,NEW.source_path,NEW.source_root,NEW.source_sha256,NEW.handler,
       NEW.override_entrypoint,NEW.override_cmd,NEW.override_env,NEW.override_env_secrets,NEW.override_port,NEW.override_healthcheck,
       NEW.override_liveness_probe,NEW.override_readiness_probe,NEW.override_main_depends_on,NEW.release_command,NEW.release_command_shell)
   IS DISTINCT FROM
   ROW(OLD.app_id,OLD.scope,OLD.kind,OLD.image_digest,OLD.commit_sha,OLD.source_path,OLD.source_root,OLD.source_sha256,OLD.handler,
       OLD.override_entrypoint,OLD.override_cmd,OLD.override_env,OLD.override_env_secrets,OLD.override_port,OLD.override_healthcheck,
       OLD.override_liveness_probe,OLD.override_readiness_probe,OLD.override_main_depends_on,OLD.release_command,OLD.release_command_shell) THEN
   RAISE EXCEPTION 'frozen environment workload inputs are immutable' USING ERRCODE='23514';
  END IF;
 ELSIF frozen IS NOT NULL THEN
  IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'environment workload inputs must be created atomically' USING ERRCODE='23514'; END IF;
  SELECT * INTO src FROM active_environment_git_sources WHERE id=(frozen->>'source_id')::uuid FOR UPDATE;
  allowed:=src.id IS NOT NULL AND src.mode='enforce' AND NOT src.suspended AND src.generation=(frozen->>'generation')::bigint
   AND src.intent_version=(frozen->>'intent_version')::bigint AND src.environment_id=(frozen->>'environment_id')::uuid
   AND src.approved_revision_id=(frozen->>'revision_id')::uuid AND EXISTS(SELECT 1 FROM environment_gitops_jobs j
    WHERE j.source_id=src.id AND j.desired_generation=src.generation AND j.claimed_generation=src.generation
     AND j.lease_until>clock_timestamp() AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true));
  IF NOT allowed OR NOT EXISTS(SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id
   JOIN project_environments e ON e.id=src.environment_id JOIN app_environment_workload_intents w ON w.app_id=a.id AND w.environment_id=e.id
   JOIN environment_desired_revisions rev ON rev.id=src.approved_revision_id AND rev.source_id=src.id
   WHERE r.source_id=src.id AND r.logical_name=frozen->>'resource' AND r.app_id=NEW.app_id AND a.status IN ('active','evicted_cold') AND a.type='app'
    AND a.account_id=src.account_id AND a.project_id=src.project_id AND e.account_id=src.account_id AND e.project_id=src.project_id
    AND NEW.app_id=(frozen->>'app_id')::uuid AND NEW.scope=frozen->>'scope' AND NEW.scope=e.slug AND NEW.status='pending' AND (
     (NEW.kind='image' AND (
       (w.source->>'kind'='image' AND NEW.image_digest=w.source->>'image') OR
       (w.source IS NULL AND frozen ? 'deployment_inputs' AND jsonb_array_length(coalesce(frozen->'source_deployments','[]'::jsonb))>0
        AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'
         AND (d.kind<>'image' OR d.image_digest IS DISTINCT FROM NEW.image_digest))))
      AND NEW.image_digest ~ '^[^[:space:]]+@sha256:[a-f0-9]{64}$') OR
     (NEW.kind='github' AND w.source->>'kind' IN ('source','dockerfile') AND w.source_revision=rev.commit_sha
      AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND f.field_path='source') AND frozen->'source'=w.source AND
      jsonb_typeof(frozen->'source_archive')='object' AND frozen->'source_archive' ?& ARRAY['build_id','path','sha256','bytes','log_path','revision_id','commit_sha','definition_digest'] AND
      frozen->'source_archive'->>'revision_id'=rev.id::text AND frozen->'source_archive'->>'commit_sha'=rev.commit_sha AND
      frozen->'source_archive'->>'definition_digest'=rev.definition_digest AND frozen->>'definition_digest'=rev.definition_digest AND
      (frozen->'source_archive'->>'build_id')::uuid IS NOT NULL AND substring(frozen->'source_archive'->>'build_id',15,1)='7' AND
      NEW.source_root=w.source->>'directory' AND NEW.source_path=frozen->'source_archive'->>'path' AND NEW.source_path LIKE '/%' AND
      NEW.source_sha256=frozen->'source_archive'->>'sha256' AND NEW.source_sha256 ~ '^[a-f0-9]{64}$' AND
      NEW.source_bytes=(frozen->'source_archive'->>'bytes')::bigint AND NEW.source_bytes>0 AND
      NEW.log_path=frozen->'source_archive'->>'log_path' AND NEW.log_path LIKE '/%' AND
      NEW.source_url='github://'||src.repository||'@'||rev.commit_sha AND NEW.build_id IS NULL AND NEW.inferred_profile IS NULL AND
      coalesce(NEW.github_source_ref,'')='' AND coalesce(NEW.image_digest,'')=''))
    AND NEW.commit_sha=rev.commit_sha AND frozen->'runtime'=w.runtime
    AND jsonb_strip_nulls(frozen->'baseline')=jsonb_strip_nulls(a.manifest)
    AND frozen->>'start_command'=coalesce(a.start_command,'')
    AND frozen->>'app_type'=a.type AND frozen->>'runtime_base'=coalesce(a.runtime,'') AND frozen->>'workload_class'=a.workload_class
    AND coalesce(a.manifest->'env','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb) AND coalesce(a.manifest->'service_bindings','[]'::jsonb) IN ('[]'::jsonb,'null'::jsonb)
    AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND (f.field_path='source' OR starts_with(f.field_path,'runtime/')))) THEN
   RAISE EXCEPTION 'environment workload preparation lost its reviewed authority' USING ERRCODE='23514';
  END IF;
  IF frozen ? 'deployment_inputs' AND (
   jsonb_typeof(frozen->'deployment_inputs') IS DISTINCT FROM 'object' OR
   frozen->'deployment_inputs' IS DISTINCT FROM environment_workload_deployment_inputs(NEW) OR
   frozen->'source_deployments' IS DISTINCT FROM coalesce((SELECT jsonb_agg(d.id::text ORDER BY d.id)
    FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.status='live'),'[]'::jsonb) OR
   NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.status='live') OR
   EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.status='live'
    AND environment_workload_deployment_inputs(d) IS DISTINCT FROM frozen->'deployment_inputs')) THEN
   RAISE EXCEPTION 'inherited environment workload inputs changed after review' USING ERRCODE='23514';
  END IF;
 END IF;
 IF frozen IS NOT NULL AND NEW.status='live' THEN
  RAISE EXCEPTION 'environment workload graph is not qualified for activation' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $_$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_graph() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE src environment_git_sources%ROWTYPE; rev environment_desired_revisions%ROWTYPE;
 member jsonb; member_count integer; names jsonb; allowed boolean;
 binding queue_bindings%ROWTYPE; consumer_ids jsonb; binding_resource text;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM active_environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
   WHERE s.id=OLD.source_id AND e.id=OLD.environment_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'environment graph journal is retained with its original source' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.source_id,NEW.environment_id,NEW.revision_id,NEW.generation,NEW.intent_version,
  NEW.plan_hash,NEW.definition_digest,NEW.members,NEW.resource_ids,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.source_id,OLD.environment_id,OLD.revision_id,OLD.generation,OLD.intent_version,
  OLD.plan_hash,OLD.definition_digest,OLD.members,OLD.resource_ids,OLD.created_at) THEN
  RAISE EXCEPTION 'reviewed environment graph inputs are immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO src FROM active_environment_git_sources WHERE id=NEW.source_id FOR UPDATE;
 allowed:=src.id IS NOT NULL AND src.environment_id=NEW.environment_id AND src.mode='enforce' AND NOT src.suspended
  AND src.generation=NEW.generation AND src.intent_version=NEW.intent_version AND src.approved_revision_id=NEW.revision_id
  AND EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true));
 SELECT * INTO rev FROM environment_desired_revisions WHERE id=NEW.revision_id AND source_id=NEW.source_id;
 IF NOT coalesce(allowed,false) OR rev.definition_digest IS DISTINCT FROM NEW.definition_digest THEN
  RAISE EXCEPTION 'environment graph preparation lost reviewed authority' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND (NEW.phase<>'preparing' OR NEW.prepared_at IS NOT NULL OR NEW.error_code<>'') THEN
  RAISE EXCEPTION 'environment graph must begin in preparation' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (OLD.phase='failed' AND ROW(NEW.phase,NEW.error_code,NEW.prepared_at) IS DISTINCT FROM ROW(OLD.phase,OLD.error_code,OLD.prepared_at)
  OR OLD.phase='prepared' AND NEW.phase='preparing'
  OR OLD.phase=NEW.phase AND ROW(NEW.error_code,NEW.prepared_at) IS DISTINCT FROM ROW(OLD.error_code,OLD.prepared_at)) THEN
  RAISE EXCEPTION 'environment graph preparation transition is invalid' USING ERRCODE='23514';
 END IF;
 SELECT count(DISTINCT value->>'resource'),coalesce(jsonb_agg(value->>'resource' ORDER BY value->>'resource'),'[]')
  INTO member_count,names FROM jsonb_array_elements(NEW.members);
 IF member_count<>jsonb_array_length(NEW.members) OR names IS DISTINCT FROM
  (SELECT coalesce(jsonb_agg('workload/'||key ORDER BY key),'[]') FROM jsonb_object_keys(rev.definition->'workloads') key) THEN
  RAISE EXCEPTION 'environment graph must include every reviewed workload exactly once' USING ERRCODE='23514';
 END IF;
 FOR member IN SELECT value FROM jsonb_array_elements(NEW.members) LOOP
  IF jsonb_typeof(member) IS DISTINCT FROM 'object' OR NOT member ?& ARRAY['resource','app_id','retained_deployments']
   OR jsonb_typeof(member->'retained_deployments') IS DISTINCT FROM 'array' OR NOT EXISTS(
    SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id JOIN project_environments e ON e.id=src.environment_id
    WHERE r.source_id=src.id AND r.logical_name=member->>'resource' AND a.id=(member->>'app_id')::uuid
     AND a.account_id=src.account_id AND a.project_id=src.project_id AND a.status IN ('active','evicted_cold')
     AND e.account_id=src.account_id AND e.project_id=src.project_id
     AND NEW.resource_ids->>r.logical_name=a.id::text) OR
   member->'retained_deployments' IS DISTINCT FROM (SELECT coalesce(jsonb_agg(d.id::text ORDER BY d.id),'[]')
    FROM deployments d JOIN project_environments e ON e.id=src.environment_id
    WHERE d.app_id=(member->>'app_id')::uuid AND d.scope=e.slug AND d.status='live') THEN
   RAISE EXCEPTION 'environment graph original workload identities changed' USING ERRCODE='23514';
  END IF;
  IF member ? 'candidate_deployment_id' THEN
   IF NOT EXISTS(SELECT 1 FROM deployments d WHERE d.id=(member->>'candidate_deployment_id')::uuid
    AND d.app_id=(member->>'app_id')::uuid AND d.environment_workload_runtime->>'source_id'=src.id::text
    AND d.environment_workload_runtime->>'environment_id'=src.environment_id::text
    AND d.environment_workload_runtime->>'revision_id'=rev.id::text
    AND d.environment_workload_runtime->>'generation'=NEW.generation::text
    AND d.environment_workload_runtime->>'intent_version'=NEW.intent_version::text
    AND d.environment_workload_runtime->>'resource'=member->>'resource'
    AND d.environment_workload_runtime->>'plan_hash'=NEW.plan_hash
    AND (NEW.phase<>'prepared' OR d.status='snapshotting' AND (coalesce(d.rootfs_path,'')<>'' OR coalesce(d.rootfs_key,'')<>''))) THEN
    RAISE EXCEPTION 'environment graph candidate does not match its reviewed inputs or artifact phase' USING ERRCODE='23514';
   END IF;
  ELSIF EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=member->>'resource'
   AND (f.field_path='source' OR starts_with(f.field_path,'runtime/'))) THEN
   RAISE EXCEPTION 'environment graph is missing a managed workload candidate' USING ERRCODE='23514';
  END IF;
  FOR binding IN SELECT b.* FROM queue_bindings b WHERE b.app_id=(member->>'app_id')::uuid
   AND b.account_id=src.account_id AND b.environment_id=src.environment_id LOOP
   binding_resource:=(member->>'resource')||'/queue_bindings/'||binding.name;
   SELECT coalesce(jsonb_agg(t.id::text ORDER BY t.id),'[]') INTO consumer_ids FROM triggers t WHERE t.queue_binding_id=binding.id;
   IF NEW.resource_ids->>binding_resource IS DISTINCT FROM binding.id::text OR
    jsonb_array_length(consumer_ids)=1 AND NEW.resource_ids->>(binding_resource||'/consumer') IS DISTINCT FROM consumer_ids->>0 OR
    jsonb_array_length(consumer_ids)<>1 AND NEW.resource_ids ? (binding_resource||'/consumer') THEN
    RAISE EXCEPTION 'environment graph original queue and consumer identities changed' USING ERRCODE='23514';
   END IF;
  END LOOP;
 END LOOP;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_instance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; app_ram integer; may_deploy boolean;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments d
   JOIN active_environment_git_sources s ON s.id=(d.environment_workload_runtime->>'source_id')::uuid
   JOIN project_environments e ON e.id=(d.environment_workload_runtime->>'environment_id')::uuid WHERE d.id=OLD.deployment_id) THEN
   SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=OLD.id FOR UPDATE;
   IF OLD.state NOT IN ('parked','stopped','failed') OR (q.id IS NOT NULL AND q.lease_until>clock_timestamp()) THEN
    RAISE EXCEPTION 'environment qualification reservation must remain until retired and its attempt released' USING ERRCODE='23514';
   END IF;
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL)
  AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.ram_mb,NEW.kind,NEW.job_id)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.ram_mb,OLD.kind,OLD.job_id) THEN
  RAISE EXCEPTION 'environment qualification instance identity is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id;
 IF q.id IS NOT NULL AND (q.app_id IS DISTINCT FROM NEW.app_id OR q.deployment_id IS DISTINCT FROM NEW.deployment_id) THEN
  RAISE EXCEPTION 'environment qualification reservation cannot be reassigned' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment qualification instances must be created under their attempt' USING ERRCODE='23514';
 END IF;
 -- Retirement must remain available after expiry, supersession and parent
 -- deletion. It cannot grant execution or qualification evidence.
 IF TG_OP='UPDATE' AND NEW.state IN ('parked','stopped','failed','evicting_account_deleting') THEN RETURN NEW; END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM active_environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id
   WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.execution_mode='job' OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>NEW.app_id OR NEW.kind<>'wake' OR NEW.job_id IS NOT NULL
  OR NEW.mode IS DISTINCT FROM (CASE q.execution_mode WHEN 'worker' THEN 'worker' WHEN 'service' THEN 'service' ELSE 'normal' END)
  OR NOT environment_workload_qualification_inputs_current(q.id) THEN
  RAISE EXCEPTION 'environment workload execution requires its current qualification attempt' USING ERRCODE='23514';
 END IF;
 SELECT a.ram_mb,c.status='active' AND c.abuse_hold_at IS NULL INTO app_ram,may_deploy
  FROM apps a JOIN accounts c ON c.id=a.account_id WHERE a.id=NEW.app_id FOR SHARE OF c;
 IF NOT coalesce(may_deploy,false) OR NEW.ram_mb IS DISTINCT FROM app_ram OR NOT EXISTS(
  SELECT 1 FROM compute_nodes WHERE id=NEW.node_id AND active AND lifecycle='active') OR
  (TG_OP='INSERT' AND (NEW.state<>'cold_booting' OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL
   OR coalesce(NEW.guest_uid,0)<>0 OR NEW.framework_ready_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'environment qualification admission shape is invalid' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
 row_value jsonb; prior jsonb; src environment_git_sources%ROWTYPE;
 resource_name text; paths text[]; controller boolean;
BEGIN
 IF TG_OP='DELETE' THEN row_value:=to_jsonb(OLD); ELSE row_value:=to_jsonb(NEW); END IF;
 -- Parent purges remove their original identity before cascading. Ordinary
 -- scoped-row deletion still requires ownership authority below.
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM apps a JOIN accounts c ON c.id=a.account_id
  JOIN project_environments e ON e.account_id=a.account_id AND e.project_id=a.project_id
  WHERE a.id=OLD.app_id AND a.account_id=OLD.account_id AND e.id=OLD.environment_id) THEN
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.environment_id IS DISTINCT FROM OLD.environment_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_workload_intent_identity',MESSAGE='scoped workload identity is immutable';
 END IF;
 IF TG_OP<>'DELETE' AND NOT EXISTS(SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
  WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id AND e.id=NEW.environment_id AND a.status<>'deleted') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_workload_intent_identity',MESSAGE='scoped workload requires its original project environment';
 END IF;
 SELECT s.* INTO src FROM active_environment_git_sources s WHERE s.account_id=(row_value->>'account_id')::uuid
  AND s.environment_id=(row_value->>'environment_id')::uuid FOR UPDATE OF s;
 -- ON CONFLICT fires the INSERT trigger before its UPDATE trigger. Compare
 -- against the original scoped row so an unchanged owned field is not an edit.
 IF TG_OP='INSERT' THEN
  SELECT to_jsonb(w) INTO prior FROM app_environment_workload_intents w
   WHERE w.app_id=NEW.app_id AND w.environment_id=NEW.environment_id FOR UPDATE OF w;
  prior:=coalesce(prior,'{}');
 ELSE prior:=to_jsonb(OLD); END IF;
 IF TG_OP='DELETE' THEN row_value:=row_value||jsonb_build_object('source',NULL,'source_revision',NULL,'runtime','{}'::jsonb); END IF;
 SELECT array_agg('runtime/'||k) INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'runtime','{}')) UNION SELECT key FROM jsonb_each(row_value->'runtime')
 ) keys WHERE (prior->'runtime'->k) IS DISTINCT FROM (row_value->'runtime'->k);
 IF coalesce(prior->'source','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source']; END IF;
 IF coalesce(prior->'source_revision','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source_revision','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source_revision','source']; END IF;
 IF coalesce(cardinality(paths),0)=0 THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=(row_value->>'app_id')::uuid;
  controller:=EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true) AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id
   AND f.environment_id=src.environment_id AND f.resource=resource_name AND f.field_path=ANY(paths)
   AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=f.environment_id
    AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at); END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_qualification_request() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE graph environment_workload_graphs%ROWTYPE; src environment_git_sources%ROWTYPE; dep deployments%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM environment_workload_graphs WHERE id=OLD.graph_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'qualification work is retained with its reviewed graph' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.graph_id,NEW.deployment_id,NEW.app_id,NEW.resource,NEW.artifact,NEW.frozen_inputs,NEW.execution_mode,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.graph_id,OLD.deployment_id,OLD.app_id,OLD.resource,OLD.artifact,OLD.frozen_inputs,OLD.execution_mode,OLD.created_at) THEN
  RAISE EXCEPTION 'environment qualification inputs are immutable' USING ERRCODE='23514';
 END IF;
 -- The state store takes source, then sorted app locks, before this row.
 SELECT s.* INTO src FROM active_environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id WHERE g.id=NEW.graph_id FOR UPDATE OF s;
 SELECT * INTO graph FROM environment_workload_graphs WHERE id=NEW.graph_id;
 SELECT * INTO dep FROM deployments WHERE id=NEW.deployment_id;
 IF src.id IS NULL OR src.mode<>'enforce' OR src.suspended OR graph.phase<>'prepared' OR graph.generation<>src.generation OR
  graph.intent_version<>src.intent_version OR graph.revision_id IS DISTINCT FROM src.approved_revision_id OR graph.environment_id<>src.environment_id OR
  dep.status<>'snapshotting' OR coalesce(dep.rootfs_bytes,0)<=0 OR coalesce(dep.rootfs_key,'')='' AND coalesce(dep.rootfs_path,'')='' OR
  NEW.artifact IS DISTINCT FROM environment_workload_artifact(dep) OR NEW.frozen_inputs IS DISTINCT FROM dep.environment_workload_runtime OR
  NEW.execution_mode IS DISTINCT FROM (CASE WHEN NEW.frozen_inputs->'runtime' ? 'execution_mode'
   THEN coalesce(nullif(NEW.frozen_inputs->'runtime'->>'execution_mode',''),'request')
   ELSE coalesce(nullif(NEW.frozen_inputs->'baseline'->>'execution_mode',''),'request') END) OR
  NOT EXISTS(SELECT 1 FROM jsonb_array_elements(graph.members) m WHERE m->>'resource'=NEW.resource AND m->>'app_id'=NEW.app_id::text
   AND m->>'candidate_deployment_id'=NEW.deployment_id::text) OR NOT EXISTS(
   SELECT 1 FROM apps a JOIN project_environments e ON e.id=graph.environment_id
   WHERE a.id=NEW.app_id AND a.id=dep.app_id AND a.status IN ('active','evicted_cold') AND a.account_id=src.account_id AND a.project_id=src.project_id
    AND e.account_id=src.account_id AND e.project_id=src.project_id AND dep.scope=e.slug
    AND jsonb_strip_nulls(NEW.frozen_inputs->'baseline')=jsonb_strip_nulls(a.manifest)
    AND NEW.frozen_inputs->>'start_command'=coalesce(a.start_command,'')
    AND NEW.frozen_inputs->>'app_type'=a.type AND NEW.frozen_inputs->>'runtime_base'=coalesce(a.runtime,'')
    AND NEW.frozen_inputs->>'workload_class'=a.workload_class) THEN
  RAISE EXCEPTION 'environment qualification lost its reviewed graph or artifact' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.phase<>'queued' OR NOT EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true)) THEN
   RAISE EXCEPTION 'environment qualification publication requires the issued intent lease' USING ERRCODE='23514';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(graph.members) m LEFT JOIN environment_workload_qualification_requests q
   ON q.graph_id=graph.id AND q.resource=m->>'resource' AND q.deployment_id=(m->>'candidate_deployment_id')::uuid
   LEFT JOIN deployments d ON d.id=q.deployment_id LEFT JOIN apps a ON a.id=d.app_id
   WHERE m ? 'candidate_deployment_id' AND (q.id IS NULL OR d.status IS DISTINCT FROM 'snapshotting'
    OR q.artifact IS DISTINCT FROM environment_workload_artifact(d) OR q.frozen_inputs IS DISTINCT FROM d.environment_workload_runtime
    OR a.status IS NULL OR a.status NOT IN ('active','evicted_cold')
    OR jsonb_strip_nulls(q.frozen_inputs->'baseline') IS DISTINCT FROM jsonb_strip_nulls(a.manifest)
    OR q.frozen_inputs->>'start_command' IS DISTINCT FROM coalesce(a.start_command,'')
    OR q.frozen_inputs->>'app_type' IS DISTINCT FROM a.type OR q.frozen_inputs->>'runtime_base' IS DISTINCT FROM coalesce(a.runtime,'')
    OR q.frozen_inputs->>'workload_class' IS DISTINCT FROM a.workload_class)) THEN
   RAISE EXCEPTION 'environment qualification requires the complete unchanged artifact cohort' USING ERRCODE='23514';
  END IF;
  IF NEW.phase<>'claimed' OR NEW.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true) OR
   NEW.lease_until<=clock_timestamp() OR NEW.lease_until>clock_timestamp()+interval '15 minutes' OR
   (OLD.phase='claimed' AND OLD.lease_until>clock_timestamp() AND
    (NEW.lease_token<>OLD.lease_token OR NEW.worker_id<>OLD.worker_id OR NEW.attempt<>OLD.attempt OR NEW.lease_until<OLD.lease_until
     OR NEW.reserved_instance_id IS DISTINCT FROM OLD.reserved_instance_id)) OR
   ((OLD.phase='queued' OR OLD.lease_until<=clock_timestamp()) AND
    (NEW.attempt<>OLD.attempt+1 OR NEW.lease_token=OLD.lease_token OR
     OLD.reserved_instance_id IS NOT NULL AND NEW.execution_mode<>'job' AND NEW.reserved_instance_id=OLD.reserved_instance_id OR
     EXISTS(SELECT 1 FROM instances i WHERE i.id=OLD.reserved_instance_id AND i.state NOT IN ('parked','stopped','failed')))) THEN
   RAISE EXCEPTION 'environment qualification execution lease is stale' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION guard_environment_git_source_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.detached AND NOT NEW.detached THEN
  RAISE EXCEPTION 'Retired Git bindings cannot be reactivated' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS environment_git_source_retirement_guard ON environment_git_sources;
CREATE TRIGGER environment_git_source_retirement_guard BEFORE UPDATE ON environment_git_sources FOR EACH ROW EXECUTE FUNCTION guard_environment_git_source_retirement();

-- Both writers lock the active source before testing physical ownership. This
-- keeps direct SQL and the API adoption/claim paths in the same lock order.
CREATE OR REPLACE FUNCTION guard_environment_external_field_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE src active_environment_git_sources%ROWTYPE;
BEGIN
 IF NEW.resource<>'environment' AND NOT EXISTS(SELECT 1 FROM apps a JOIN project_environments e ON e.account_id=a.account_id AND e.project_id=a.project_id
  WHERE e.id=NEW.environment_id AND NEW.resource='app/'||a.id::text AND a.status<>'deleted') THEN
  RAISE EXCEPTION 'External field ownership must target the scoped app' USING ERRCODE='23514';
 END IF;
 SELECT * INTO src FROM active_environment_git_sources WHERE environment_id=NEW.environment_id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM environment_managed_fields f LEFT JOIN environment_gitops_resources r ON r.source_id=f.source_id AND r.logical_name=f.resource
  WHERE f.source_id=src.id AND (NEW.resource='environment' AND f.resource='environment' OR NEW.resource='app/'||r.app_id::text)
   AND (f.field_path=NEW.field_path OR starts_with(NEW.field_path,'variables/') AND f.field_path='secret_refs/'||substring(NEW.field_path FROM 11))) THEN
  RAISE EXCEPTION 'Field already has a Git owner' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS environment_external_field_owner_guard ON environment_external_field_owners;
CREATE TRIGGER environment_external_field_owner_guard BEFORE INSERT OR UPDATE ON environment_external_field_owners FOR EACH ROW EXECUTE FUNCTION guard_environment_external_field_owner();
CREATE OR REPLACE FUNCTION guard_environment_git_field_foreign_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid;
BEGIN
 IF NEW.manager_kind<>'git' THEN RETURN NEW; END IF;
 PERFORM id FROM active_environment_git_sources WHERE id=NEW.source_id FOR UPDATE;
 SELECT app_id INTO app FROM environment_gitops_resources WHERE source_id=NEW.source_id AND logical_name=NEW.resource;
 IF EXISTS(SELECT 1 FROM environment_external_field_owners f WHERE f.environment_id=NEW.environment_id
  AND (NEW.resource='environment' AND f.resource='environment' OR f.resource='app/'||app::text)
  AND (f.field_path=NEW.field_path OR starts_with(NEW.field_path,'secret_refs/') AND f.field_path='variables/'||substring(NEW.field_path FROM 13))) THEN
  RAISE EXCEPTION 'Field already has an external owner' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS environment_git_field_foreign_owner_guard ON environment_managed_fields;
CREATE TRIGGER environment_git_field_foreign_owner_guard BEFORE INSERT OR UPDATE ON environment_managed_fields FOR EACH ROW EXECUTE FUNCTION guard_environment_git_field_foreign_owner();

-- +goose StatementEnd
-- +goose Down
-- Retain binding history and ownership; old readers must fail closed.
SELECT 1;
