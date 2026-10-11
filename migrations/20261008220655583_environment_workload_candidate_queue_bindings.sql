-- filename: 20261008220655583_environment_workload_candidate_queue_bindings.sql

-- +goose Up
-- A candidate is tied to the exact reviewed queue namespace. Binding UUIDs
-- are immutable identities; contracts are compared both with the approved
-- revision and the active scoped database row before candidate insertion.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_candidate_queue_bindings_valid(input_value jsonb, candidate_app_id uuid) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $$
DECLARE source_row environment_git_sources%ROWTYPE; revision_row environment_desired_revisions%ROWTYPE;
        approved_bindings jsonb; frozen_bindings jsonb; workload_name text; entry record;
BEGIN
 BEGIN
  SELECT * INTO source_row FROM environment_git_sources WHERE id=(input_value->>'source_id')::uuid;
  SELECT * INTO revision_row FROM environment_desired_revisions
   WHERE id=(input_value->>'revision_id')::uuid AND source_id=source_row.id;
 EXCEPTION WHEN invalid_text_representation THEN
  RETURN false;
 END;
 IF source_row.id IS NULL OR revision_row.id IS NULL THEN RETURN false; END IF;
 IF coalesce(input_value->>'resource','') !~ '^workload/[a-z0-9][a-z0-9-]*$' THEN RETURN false; END IF;
 workload_name:=substring(input_value->>'resource' from 10);
 approved_bindings:=coalesce(revision_row.definition->'workloads'->workload_name->'queue_bindings','{}'::jsonb);
 frozen_bindings:=coalesce(input_value->'queue_bindings','{}'::jsonb);
 IF jsonb_typeof(approved_bindings) IS DISTINCT FROM 'object' OR jsonb_typeof(frozen_bindings) IS DISTINCT FROM 'object' THEN
  RETURN false;
 END IF;
 IF (SELECT count(*) FROM jsonb_each(approved_bindings)) <>
    (SELECT count(*) FROM jsonb_each(frozen_bindings)) THEN RETURN false; END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(frozen_bindings) LOOP
  IF jsonb_typeof(entry.value) IS DISTINCT FROM 'object' OR NOT (entry.value ?& ARRAY['binding_id','contract']) OR
     jsonb_typeof(entry.value->'contract') IS DISTINCT FROM 'object' OR
     coalesce(entry.value->>'binding_id','') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR
     entry.value->'contract' IS DISTINCT FROM approved_bindings->entry.key THEN RETURN false; END IF;
  IF NOT EXISTS(
   SELECT 1 FROM environment_gitops_queue_bindings identity
   JOIN environment_managed_fields owner ON owner.source_id=identity.source_id AND owner.environment_id=source_row.environment_id
    AND owner.resource=identity.resource AND owner.field_path=identity.field_path
   JOIN queue_bindings binding ON binding.id=identity.binding_id
   JOIN apps app ON app.id=binding.app_id
   WHERE identity.source_id=source_row.id AND identity.resource=input_value->>'resource'
    AND identity.field_path='queue_bindings/'||entry.key AND identity.binding_id=(entry.value->>'binding_id')::uuid
    AND binding.id=(entry.value->>'binding_id')::uuid AND binding.account_id=source_row.account_id
    AND binding.app_id=candidate_app_id AND binding.environment_id=source_row.environment_id
    AND binding.deployment_scope=input_value->>'scope' AND binding.name=entry.key AND binding.retired_at IS NULL
    AND binding.workload_class=app.workload_class AND binding.workload_class=entry.value->'contract'->>'workload_class'
    AND entry.value->'contract'=(jsonb_build_object('queue_name',binding.queue_name,'mode',binding.mode,
      'workload_class',binding.workload_class,'enabled',binding.enabled,'max_concurrency',binding.max_concurrency)
      || CASE WHEN binding.retry_policy='{}'::jsonb THEN '{}'::jsonb ELSE jsonb_build_object('retry_policy',binding.retry_policy) END)
  ) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
-- +goose StatementEnd

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
    starts_with(step->>'path','service_bindings/') OR starts_with(step->>'path','queue_bindings/'))
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
    AND jsonb_strip_nulls(frozen->'baseline')=jsonb_strip_nulls(a.manifest)
    AND frozen->>'start_command'=coalesce(a.start_command,'')
    AND frozen->>'app_type'=a.type AND frozen->>'runtime_base'=coalesce(a.runtime,'') AND frozen->>'workload_class'=a.workload_class
    AND coalesce(a.manifest->'env','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb) AND coalesce(a.manifest->'service_bindings','[]'::jsonb) IN ('[]'::jsonb,'null'::jsonb)
    AND environment_workload_candidate_queue_bindings_valid(frozen, NEW.app_id)
    AND (EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND (f.field_path IN ('source','source_revision') OR starts_with(f.field_path,'runtime/') OR starts_with(f.field_path,'service_bindings/') OR starts_with(f.field_path,'queue_bindings/')))
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
  RAISE EXCEPTION 'environment workload graph is not qualified for activation' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $_$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: existing candidate rows retain their frozen authority contract.
SELECT 1;
-- +goose StatementEnd
