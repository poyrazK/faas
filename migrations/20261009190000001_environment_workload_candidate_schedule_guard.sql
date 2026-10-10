-- +goose Up
-- Keep schedule and per-key variable removals and candidate snapshots under
-- the same database fence as reviewed workload fields. Dispatch remains gated.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_candidate_applied_removal_valid(input_value jsonb) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $$
DECLARE source_uuid uuid; revision_uuid uuid; source_generation bigint; resource_name text;
BEGIN
 BEGIN
  source_uuid:=(input_value->>'source_id')::uuid;
  revision_uuid:=(input_value->>'revision_id')::uuid;
  source_generation:=(input_value->>'generation')::bigint;
  resource_name:=input_value->>'resource';
 EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
  RETURN false;
 END;
 IF source_uuid IS NULL OR revision_uuid IS NULL OR source_generation IS NULL OR resource_name IS NULL THEN RETURN false; END IF;
 RETURN EXISTS(
  SELECT 1 FROM environment_gitops_runs run CROSS JOIN LATERAL jsonb_array_elements(run.steps) step
  WHERE run.source_id=source_uuid AND run.revision_id=revision_uuid AND run.generation=source_generation
   AND step->>'resource'=resource_name AND step->>'status'='applied' AND step->>'action'='remove'
   AND (step->>'path' IN ('source','source_revision') OR starts_with(step->>'path','runtime/') OR
    starts_with(step->>'path','service_bindings/') OR starts_with(step->>'path','queue_bindings/') OR
    starts_with(step->>'path','variables/') OR step->>'path'='schedule')
 );
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_candidate() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE frozen jsonb; src environment_git_sources%ROWTYPE; allowed boolean;
BEGIN
 frozen:=NEW.environment_workload_runtime;
 IF NEW.status='live' THEN
  IF TG_OP='INSERT' THEN
   IF EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL)
    AND NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'GitOps-managed workload promotion requires graph activation' USING ERRCODE='23514';
   END IF;
  ELSIF TG_OP='UPDATE' THEN
   IF OLD.status IS DISTINCT FROM 'live' AND
    EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL)
    AND NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'GitOps-managed workload promotion requires graph activation' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL AND OLD.status='live' AND
  NEW.status NOT IN ('live','failed') THEN
  RAISE EXCEPTION 'activated environment workload status is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL AND OLD.status='live' AND
  ROW(NEW.rootfs_path,NEW.rootfs_key,NEW.rootfs_bytes) IS DISTINCT FROM ROW(OLD.rootfs_path,OLD.rootfs_key,OLD.rootfs_bytes) THEN
  RAISE EXCEPTION 'activated environment workload artifact is immutable' USING ERRCODE='23514';
 END IF;
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
   JOIN project_environments e ON e.id=src.environment_id LEFT JOIN app_environment_workload_intents w ON w.app_id=a.id AND w.environment_id=e.id
   JOIN environment_desired_revisions rev ON rev.id=src.approved_revision_id AND rev.source_id=src.id
   WHERE r.source_id=src.id AND r.logical_name=frozen->>'resource' AND r.app_id=NEW.app_id AND a.status IN ('active','evicted_cold') AND (a.type='app' OR a.type='function' AND coalesce(a.runtime,'') IN ('node22','python312','go124','go124-alpine','node24','python313'))
    AND a.account_id=src.account_id AND a.project_id=src.project_id AND e.account_id=src.account_id AND e.project_id=src.project_id
    AND NEW.app_id=(frozen->>'app_id')::uuid AND NEW.scope=frozen->>'scope' AND NEW.scope=e.slug AND NEW.status='pending' AND (
     (NEW.kind='image' AND (
       (w.source->>'kind'='image' AND NEW.image_digest=w.source->>'image') OR
       (w.source IS NULL AND frozen ? 'deployment_inputs' AND jsonb_array_length(coalesce(frozen->'source_deployments','[]'::jsonb))>0
        AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'
         AND (d.kind<>'image' OR d.image_digest IS DISTINCT FROM NEW.image_digest))))
      AND NEW.image_digest ~ '^[^[:space:]]+@sha256:[a-f0-9]{64}$') OR
     (NEW.kind='github' AND w.source->>'kind' IN ('source','dockerfile','function') AND
      (w.source->>'kind'<>'function' OR a.type='function' AND a.runtime=w.source->>'runtime') AND w.source_revision=rev.commit_sha
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
    AND NEW.commit_sha=rev.commit_sha AND frozen->'runtime'=coalesce(w.runtime,'{}'::jsonb) AND coalesce(frozen->'service_bindings','{}'::jsonb)=coalesce(w.service_bindings,'{}'::jsonb)
    AND coalesce(frozen->'variables','{}'::jsonb)=coalesce(w.variables,'{}'::jsonb)
    AND coalesce(frozen->'schedule','null'::jsonb)=coalesce(w.schedule,'null'::jsonb)
    AND jsonb_strip_nulls(frozen->'baseline')=jsonb_strip_nulls(a.manifest)
    AND frozen->>'start_command'=coalesce(a.start_command,'')
    AND frozen->>'app_type'=a.type AND frozen->>'runtime_base'=coalesce(a.runtime,'') AND frozen->>'workload_class'=a.workload_class
    AND coalesce(a.manifest->'env','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb) AND coalesce(a.manifest->'service_bindings','[]'::jsonb) IN ('[]'::jsonb,'null'::jsonb)
    AND environment_workload_candidate_queue_bindings_valid(frozen, NEW.app_id)
    AND (EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND (f.field_path IN ('source','source_revision','schedule') OR starts_with(f.field_path,'runtime/') OR starts_with(f.field_path,'service_bindings/') OR starts_with(f.field_path,'queue_bindings/') OR starts_with(f.field_path,'variables/')))
     OR environment_workload_candidate_applied_removal_valid(frozen))) THEN
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
  IF TG_OP='INSERT' OR OLD.status IS DISTINCT FROM 'live' THEN
   IF NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'environment workload graph is not qualified for activation' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $_$;
-- +goose StatementEnd

-- +goose Down
-- This guard participates in activated-candidate immutability. Keep it in
-- place on downgrade rather than restoring a trigger that lacks the schedule
-- removal fence; all workload hold and activation authority stays fail-closed.
SELECT 1;
