-- ADR-429: inherit the reviewed deployment inputs without owning its source.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_deployment_inputs(d deployments) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT jsonb_build_object(
  'override_entrypoint',coalesce((d).override_entrypoint,ARRAY[]::text[]),'override_cmd',coalesce((d).override_cmd,ARRAY[]::text[]),
  'override_env',coalesce(nullif((d).override_env,'null'::jsonb),'{}'::jsonb),'override_env_secrets',coalesce(nullif((d).override_env_secrets,'null'::jsonb),'{}'::jsonb),
  'override_port',coalesce((d).override_port,0),'override_healthcheck',coalesce(nullif((d).override_healthcheck,'null'::jsonb),'{}'::jsonb),
  'override_liveness_probe',coalesce(nullif((d).override_liveness_probe,'null'::jsonb),'{}'::jsonb),'override_readiness_probe',coalesce(nullif((d).override_readiness_probe,'null'::jsonb),'{}'::jsonb),
  'override_main_depends_on',(d).override_main_depends_on,
  'sidecars',coalesce((SELECT jsonb_agg(sc-'secret_reload_signal' ORDER BY position) FROM jsonb_array_elements((d).sidecars) WITH ORDINALITY AS s(sc,position)),'[]'::jsonb),
  'workflows',(d).workflows,
  'full_rootfs_allow_auto',(d).full_rootfs_allow_auto,'full_rootfs_override',(d).full_rootfs_override,
  'min_instances',(d).min_instances,'release_command',(d).release_command,'release_command_shell',(d).release_command_shell,
  'disable_startup_cpu_boost',(d).disable_startup_cpu_boost,
  'rollback_on_5xx',(d).rollback_on_5xx);
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_candidate() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE frozen jsonb; src environment_git_sources%ROWTYPE; allowed boolean;
BEGIN
 frozen:=NEW.environment_workload_runtime;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL THEN
  IF NEW.environment_workload_runtime IS DISTINCT FROM OLD.environment_workload_runtime OR
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
  SELECT * INTO src FROM environment_git_sources WHERE id=(frozen->>'source_id')::uuid FOR UPDATE;
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
    AND NEW.app_id=(frozen->>'app_id')::uuid AND NEW.scope=frozen->>'scope' AND NEW.scope=e.slug AND NEW.kind='image' AND NEW.status='pending'
    AND ((w.source->>'kind'='image' AND NEW.image_digest=w.source->>'image') OR
     (w.source IS NULL AND frozen ? 'deployment_inputs' AND jsonb_array_length(coalesce(frozen->'source_deployments','[]'::jsonb))>0
      AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'
       AND (d.kind<>'image' OR d.image_digest IS DISTINCT FROM NEW.image_digest))))
    AND NEW.image_digest ~ '^[^[:space:]]+@sha256:[a-f0-9]{64}$' AND NEW.commit_sha=rev.commit_sha AND frozen->'runtime'=w.runtime
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
END $$;
-- +goose StatementEnd
-- Live source and deployment inputs participate in review. The existing
-- source-first lock fences preparation against console and direct SQL writes.
DROP TRIGGER IF EXISTS environment_secret_reference_baseline ON deployments;
CREATE TRIGGER environment_secret_reference_baseline BEFORE INSERT OR DELETE OR UPDATE OF
 status,scope,app_id,kind,image_digest,override_entrypoint,override_cmd,override_env,override_env_secrets,override_port,
 override_healthcheck,override_liveness_probe,override_readiness_probe,override_main_depends_on,sidecars,workflows,
 full_rootfs_allow_auto,full_rootfs_override,min_instances,release_command,release_command_shell,disable_startup_cpu_boost,
 rollback_on_5xx ON deployments FOR EACH ROW EXECUTE FUNCTION guard_environment_secret_reference_baseline();
-- +goose Down
-- Keep frozen baselines and the execution hold; rollback never releases them.
SELECT 1;
