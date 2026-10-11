-- ADR-568: release a prepared GitOps cohort only under its live lease.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_activation_authorized(candidate deployments) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
DECLARE frozen jsonb; token text; source_uuid uuid; source_generation bigint; source_intent bigint; environment_uuid uuid; revision_uuid uuid;
BEGIN
 frozen:=candidate.environment_workload_runtime;
 IF frozen IS NULL OR candidate.environment_workload_held OR candidate.status<>'live' THEN RETURN false; END IF;
 token:=nullif(current_setting('gregale.gitops_activation',true),'');
 IF token IS NULL OR token IS DISTINCT FROM current_setting('gregale.gitops_lease',true) THEN RETURN false; END IF;
 BEGIN
  source_uuid:=(frozen->>'source_id')::uuid;
  source_generation:=(frozen->>'generation')::bigint;
  source_intent:=(frozen->>'intent_version')::bigint;
  environment_uuid:=(frozen->>'environment_id')::uuid;
  revision_uuid:=(frozen->>'revision_id')::uuid;
 EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
  RETURN false;
 END;
 RETURN EXISTS(
  SELECT 1 FROM active_environment_git_sources src
  JOIN environment_gitops_jobs job ON job.source_id=src.id
  JOIN environment_workload_graphs graph ON graph.source_id=src.id
  CROSS JOIN LATERAL jsonb_array_elements(graph.members) member
  WHERE src.id=source_uuid AND src.mode='enforce' AND NOT src.suspended
   AND src.generation=source_generation AND src.intent_version=source_intent
   AND src.environment_id=environment_uuid AND src.approved_revision_id=revision_uuid
   AND graph.environment_id=src.environment_id AND graph.revision_id=src.approved_revision_id
   AND graph.generation=src.generation AND graph.intent_version=src.intent_version
   AND graph.plan_hash=frozen->>'plan_hash' AND graph.phase='prepared'
   AND member->>'resource'=frozen->>'resource' AND member->>'app_id'=candidate.app_id::text
   AND member->>'candidate_deployment_id'=candidate.id::text
   AND job.desired_generation=src.generation AND job.claimed_generation=src.generation
   AND job.lease_until>clock_timestamp() AND job.lease_token=token AND token<>''
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
  IF TG_OP='INSERT' OR OLD.status IS DISTINCT FROM 'live' THEN
   IF NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'environment workload graph is not qualified for activation' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $_$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_hold_state() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.environment_workload_runtime IS NOT NULL AND NOT NEW.environment_workload_held THEN
   RAISE EXCEPTION 'environment workload candidates must start held' USING ERRCODE='23514';
  END IF;
 ELSIF OLD.environment_workload_runtime IS NOT NULL AND
   NEW.environment_workload_held IS DISTINCT FROM OLD.environment_workload_held THEN
  IF NOT (OLD.environment_workload_held AND NOT NEW.environment_workload_held AND
   environment_workload_activation_authorized(NEW)) THEN
   RAISE EXCEPTION 'environment workload hold requires graph activation' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;


-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_instance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; app_ram integer; may_deploy boolean; b environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM environment_qualification_executions WHERE instance_id=NEW.id) THEN
  RAISE EXCEPTION 'retained qualification instance cannot be reused' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments d
   JOIN environment_git_sources s ON s.id=(d.environment_workload_runtime->>'source_id')::uuid
   JOIN project_environments e ON e.id=(d.environment_workload_runtime->>'environment_id')::uuid WHERE d.id=OLD.deployment_id) THEN
   SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=OLD.id OR id=(SELECT request_id FROM environment_qualification_restore_reservations WHERE instance_id=OLD.id) FOR UPDATE;
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
 SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=NEW.id;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id OR
  (b.instance_id IS NOT NULL AND id=b.request_id AND reserved_instance_id=b.capture_instance_id AND attempt=b.attempt);
 IF q.id IS NOT NULL AND (q.app_id IS DISTINCT FROM NEW.app_id OR q.deployment_id IS DISTINCT FROM NEW.deployment_id) THEN
  RAISE EXCEPTION 'environment qualification reservation cannot be reassigned' USING ERRCODE='23514';
 END IF;
 IF q.id IS NULL AND EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND app_id=NEW.app_id
  AND environment_workload_runtime IS NOT NULL AND NOT environment_workload_held AND status='live') THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment qualification instances must be created under their attempt' USING ERRCODE='23514';
 END IF;
 -- Retirement must remain available after expiry, supersession and parent
 -- deletion. It cannot grant execution or qualification evidence.
 IF TG_OP='UPDATE' AND NEW.state IN ('parked','stopped','failed','evicting_account_deleting') THEN RETURN NEW; END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id
   WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=q.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR (q.execution_mode='job' AND
    (coalesce(jsonb_typeof(q.frozen_inputs->'job_smoke'),'null')<>'object' OR
     coalesce(nullif(q.frozen_inputs->'queue_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb
     OR NOT EXISTS(SELECT 1 FROM environment_workload_graphs g, jsonb_array_elements(g.members) member
       WHERE g.id=q.graph_id AND member->>'resource'=q.resource AND member->>'execution_mode'='job'
        AND coalesce((member->>'job_smoke_configured')::boolean,false)
        AND coalesce(nullif(member->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)
        AND coalesce((member->>'service_bindings_configured')::boolean,false)=(coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb)
        AND coalesce(member->>'queue_bindings_configured','false')='false'
        AND coalesce(nullif(member->'queue_bindings','null'::jsonb),'{}'::jsonb)='{}'::jsonb
        AND coalesce(nullif(member->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb))) OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>NEW.app_id OR NEW.kind<>'wake' OR NEW.job_id IS NOT NULL
  OR NEW.mode IS DISTINCT FROM (CASE q.execution_mode WHEN 'worker' THEN 'worker' WHEN 'service' THEN 'service' ELSE 'normal' END)
  OR (b.instance_id IS NOT NULL AND (NOT environment_qualification_restore_current(q.id,b.capture_instance_id)
   OR NEW.wake_id=(SELECT (frame->>'wake_id')::uuid FROM environment_qualification_executions WHERE instance_id=b.capture_instance_id)))
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
-- +goose StatementEnd
-- +goose Down
-- Forward-only: restoring the unconditional hold would leave already activated
-- deployments live while blocking their serving instance lifecycle.
SELECT 1;
