-- ADR-429: frozen inputs and a fail-closed hold for environment graph candidates.
-- +goose Up
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS environment_workload_runtime jsonb;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='deployments'::regclass AND conname='deployments_environment_workload_runtime_shape') THEN
  ALTER TABLE deployments ADD CONSTRAINT deployments_environment_workload_runtime_shape CHECK(environment_workload_runtime IS NULL OR
   (jsonb_typeof(environment_workload_runtime)='object' AND
    environment_workload_runtime ?& ARRAY['source_id','environment_id','revision_id','generation','intent_version','resource','plan_hash','app_id','scope','baseline','start_command','runtime'] AND
    jsonb_typeof(environment_workload_runtime->'runtime')='object' AND jsonb_typeof(environment_workload_runtime->'baseline')='object' AND
    (environment_workload_runtime->>'generation')::bigint>0 AND (environment_workload_runtime->>'intent_version')::bigint>=0 AND
    environment_workload_runtime->>'plan_hash' ~ '^[a-f0-9]{64}$' AND environment_workload_runtime->>'resource' ~ '^workload/[a-z0-9][a-z0-9-]*$'));
 END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX IF NOT EXISTS deployments_environment_workload_candidate_input ON deployments
 ((environment_workload_runtime->>'source_id'),(environment_workload_runtime->>'generation'),
  (environment_workload_runtime->>'resource'),(environment_workload_runtime->>'plan_hash'))
 WHERE environment_workload_runtime IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_candidate() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE frozen jsonb; src environment_git_sources%ROWTYPE; allowed boolean;
BEGIN
 frozen:=NEW.environment_workload_runtime;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL THEN
  IF NEW.environment_workload_runtime IS DISTINCT FROM OLD.environment_workload_runtime OR
   ROW(NEW.app_id,NEW.scope,NEW.kind,NEW.image_digest,NEW.commit_sha,NEW.source_path,NEW.source_root,NEW.source_sha256,NEW.handler,
       NEW.override_entrypoint,NEW.override_cmd,NEW.override_env,NEW.override_env_secrets,NEW.override_port,NEW.override_healthcheck,
       NEW.override_liveness_probe,NEW.override_readiness_probe,NEW.override_main_depends_on,NEW.sidecars,NEW.release_command,NEW.release_command_shell)
   IS DISTINCT FROM
   ROW(OLD.app_id,OLD.scope,OLD.kind,OLD.image_digest,OLD.commit_sha,OLD.source_path,OLD.source_root,OLD.source_sha256,OLD.handler,
       OLD.override_entrypoint,OLD.override_cmd,OLD.override_env,OLD.override_env_secrets,OLD.override_port,OLD.override_healthcheck,
       OLD.override_liveness_probe,OLD.override_readiness_probe,OLD.override_main_depends_on,OLD.sidecars,OLD.release_command,OLD.release_command_shell) THEN
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
    AND NEW.image_digest=w.source->>'image' AND w.source->>'kind'='image' AND NEW.commit_sha=rev.commit_sha AND frozen->'runtime'=w.runtime
    AND jsonb_strip_nulls(frozen->'baseline')=jsonb_strip_nulls(a.manifest)
    AND frozen->>'start_command'=coalesce(a.start_command,'')
    AND frozen->>'app_type'=a.type AND frozen->>'runtime_base'=coalesce(a.runtime,'') AND frozen->>'workload_class'=a.workload_class
    AND coalesce(a.manifest->'env','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb) AND coalesce(a.manifest->'service_bindings','[]'::jsonb) IN ('[]'::jsonb,'null'::jsonb)
    AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND (f.field_path='source' OR starts_with(f.field_path,'runtime/')))) THEN
   RAISE EXCEPTION 'environment workload preparation lost its reviewed authority' USING ERRCODE='23514';
  END IF;
 END IF;
 IF frozen IS NOT NULL AND NEW.status='live' THEN
  RAISE EXCEPTION 'environment workload graph is not qualified for activation' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_candidate ON deployments;
CREATE TRIGGER guard_environment_workload_candidate BEFORE INSERT OR UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_candidate();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_instance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM deployments d WHERE d.id=NEW.deployment_id AND d.environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment workload execution requires graph qualification' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_instance ON instances;
CREATE TRIGGER guard_environment_workload_instance BEFORE INSERT OR UPDATE OF deployment_id ON instances FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_instance();

-- +goose Down
-- Preserve immutable inputs and the hold during rollback; clearing either can
-- make an unfinished graph executable through ordinary deployment paths.
SELECT 1;
