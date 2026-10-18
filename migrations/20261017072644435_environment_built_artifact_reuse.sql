-- filename: 20261017072644435_environment_built_artifact_reuse.sql
-- ADR-20261017190804: Immutable code reuse with independent target authority.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $reuse_migration$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='deployments'::regclass AND tgname='environment_build_artifact_origin_guard' AND NOT tgisinternal) THEN RETURN; END IF;
ALTER TABLE app_environment_workload_intents ADD CONSTRAINT environment_build_artifact_revision_exclusive
 CHECK (source_revision IS NULL OR NOT (source ? 'build_artifact_id'));

CREATE TABLE environment_build_artifact_pins (
 source_id uuid REFERENCES environment_git_sources(id) ON DELETE CASCADE,
 candidate_id uuid REFERENCES deployments(id) ON DELETE CASCADE,
 artifact_id uuid NOT NULL REFERENCES source_build_rootfs(id) ON DELETE RESTRICT,
 CHECK ((source_id IS NULL) <> (candidate_id IS NULL)),
 UNIQUE(source_id,artifact_id), UNIQUE(candidate_id,artifact_id)
);

-- A manifest pin prevents GC before preparation; a candidate pin retains origin
-- provenance through rollback/history. Neither pin is qualification authority.
CREATE FUNCTION environment_pin_build_artifact(artifact uuid, expected_hash text, account uuid, project uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE; d deployments%ROWTYPE; a apps%ROWTYPE; retained text;
BEGIN
 SELECT * INTO f FROM source_build_rootfs WHERE id=artifact FOR SHARE;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR SHARE;
 SELECT * INTO a FROM apps WHERE id=d.app_id FOR SHARE;
 IF (f.input_hash=expected_hash AND a.account_id=account AND a.project_id=project AND a.status<>'deleted'
  AND d.deleted_at IS NULL AND f.input_snapshot->>'storage_key'=d.rootfs_key
  AND f.input_snapshot->>'rootfs_path'=d.rootfs_path AND (f.input_snapshot->>'content_bytes')::bigint=d.rootfs_bytes
  AND d.environment_workload_runtime->'source_archive' IS NOT NULL) IS NOT TRUE THEN
  RAISE EXCEPTION 'build artifact is unavailable' USING ERRCODE='23514';
 END IF;
 INSERT INTO layer_artifact_retention(storage_key) VALUES(f.input_snapshot->>'storage_key') ON CONFLICT DO NOTHING;
 SELECT state INTO retained FROM layer_artifact_retention WHERE storage_key=f.input_snapshot->>'storage_key' FOR UPDATE;
 IF retained IS DISTINCT FROM 'retained' THEN RAISE EXCEPTION 'build artifact is retiring' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION environment_build_artifact_source_pins() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE w jsonb; wanted jsonb;
BEGIN
 -- Serialize the pin writer with origin deletion/update and the GC key fence.
 DELETE FROM environment_build_artifact_pins WHERE source_id=NEW.id;
 IF NOT NEW.detached AND NEW.approved_revision_id IS NOT NULL THEN
  SELECT definition INTO wanted FROM environment_desired_revisions WHERE id=NEW.approved_revision_id AND source_id=NEW.id;
  FOR w IN SELECT value->'source' FROM jsonb_each(wanted->'workloads') WHERE value->'source' ? 'build_artifact_id' LOOP
   IF w->>'kind' NOT IN ('source','dockerfile','function') OR w->>'build_artifact_hash' !~ '^[a-f0-9]{64}$' THEN
    RAISE EXCEPTION 'invalid build artifact reference' USING ERRCODE='23514';
   END IF;
   PERFORM environment_pin_build_artifact((w->>'build_artifact_id')::uuid,w->>'build_artifact_hash',NEW.account_id,NEW.project_id);
   INSERT INTO environment_build_artifact_pins(source_id,artifact_id) VALUES(NEW.id,(w->>'build_artifact_id')::uuid) ON CONFLICT DO NOTHING;
  END LOOP;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_build_artifact_source_pins AFTER INSERT OR UPDATE OF approved_revision_id,detached ON environment_git_sources
 FOR EACH ROW EXECUTE FUNCTION environment_build_artifact_source_pins();

CREATE FUNCTION environment_build_artifact_valid(d deployments, materialized boolean) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE b jsonb:=d.environment_workload_runtime->'built_artifact'; f source_build_rootfs%ROWTYPE; origin deployments%ROWTYPE; a apps%ROWTYPE; src environment_git_sources%ROWTYPE;
BEGIN
 SELECT * INTO f FROM source_build_rootfs WHERE id=(b->>'id')::uuid FOR SHARE NOWAIT;
 SELECT * INTO origin FROM deployments WHERE id=f.deployment_id FOR SHARE NOWAIT;
 SELECT * INTO a FROM apps WHERE id=d.app_id FOR SHARE NOWAIT;
 SELECT * INTO src FROM environment_git_sources WHERE id=(d.environment_workload_runtime->>'source_id')::uuid;
 RETURN (b IS NOT NULL AND d.environment_workload_runtime->'source_archive' IS NULL
  AND d.environment_workload_runtime->'source'->>'build_artifact_id'=f.id::text AND d.environment_workload_runtime->'source'->>'build_artifact_hash'=f.input_hash
  AND EXISTS(SELECT 1 FROM source_build_rootfs_current c WHERE c.deployment_id=origin.id AND c.artifact_id=f.id)
  AND b->>'input_hash'=f.input_hash AND b->>'deployment_id'=origin.id::text AND b->>'build_id'=origin.build_id::text AND b->>'commit_sha'=origin.commit_sha
  AND b->>'digest'=f.input_snapshot->>'artifact_digest' AND b->>'storage_key'=f.input_snapshot->>'storage_key'
  AND b->>'rootfs_path'=f.input_snapshot->>'rootfs_path' AND b->'bytes'=f.input_snapshot->'content_bytes'
  AND origin.app_id=d.app_id AND origin.scope<>d.scope AND origin.deleted_at IS NULL AND a.status<>'deleted'
  AND a.account_id=src.account_id AND a.project_id=src.project_id AND f.input_snapshot->>'app_id'=a.id::text
  AND origin.environment_workload_runtime->'source_archive' IS NOT NULL AND origin.environment_workload_runtime->'built_artifact' IS NULL
  AND ((d.environment_workload_runtime->'source') - 'build_artifact_id'::text - 'build_artifact_hash'::text)=origin.environment_workload_runtime->'source'
  AND coalesce(d.environment_workload_runtime->>'source_provider','')=coalesce(origin.environment_workload_runtime->>'source_provider','')
  AND d.environment_workload_runtime->>'source_repository_id'=origin.environment_workload_runtime->>'source_repository_id'
  AND d.environment_workload_runtime->>'source_repository'=origin.environment_workload_runtime->>'source_repository'
  AND coalesce(d.sidecars,'[]'::jsonb)='[]'::jsonb
  AND d.build_id IS NULL AND coalesce(d.image_digest,'')='' AND coalesce(d.source_path,'')='' AND coalesce(d.source_sha256,'')='' AND d.source_bytes=0 AND coalesce(d.log_path,'')=''
  AND (NOT materialized OR (d.rootfs_key=b->>'storage_key' AND d.rootfs_path=b->>'rootfs_path' AND d.rootfs_bytes=(b->>'bytes')::bigint
   AND EXISTS(SELECT 1 FROM environment_build_artifact_pins p WHERE p.candidate_id=d.id AND p.artifact_id=f.id)))) IS TRUE;
END $$;

CREATE FUNCTION environment_build_artifact_candidate_pin() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b jsonb:=NEW.environment_workload_runtime->'built_artifact'; a apps%ROWTYPE;
BEGIN
 IF b IS NOT NULL THEN
  IF (TG_OP='INSERT' OR ROW(NEW.rootfs_path,NEW.rootfs_key,NEW.rootfs_bytes) IS DISTINCT FROM ROW(OLD.rootfs_path,OLD.rootfs_key,OLD.rootfs_bytes)) AND NOT environment_build_artifact_valid(NEW,TG_OP='UPDATE') THEN RAISE EXCEPTION 'borrowed build artifact changed' USING ERRCODE='23514'; END IF;
  IF TG_OP='INSERT' THEN
   SELECT * INTO a FROM apps WHERE id=NEW.app_id;
   PERFORM environment_pin_build_artifact((b->>'id')::uuid,b->>'input_hash',a.account_id,a.project_id);
   INSERT INTO environment_build_artifact_pins(candidate_id,artifact_id) VALUES(NEW.id,(b->>'id')::uuid);
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_build_artifact_candidate_pin AFTER INSERT OR UPDATE ON deployments
 FOR EACH ROW EXECUTE FUNCTION environment_build_artifact_candidate_pin();

CREATE FUNCTION environment_build_artifact_origin_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM source_build_rootfs f JOIN environment_build_artifact_pins p ON p.artifact_id=f.id WHERE f.deployment_id=OLD.id) THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'build artifact provenance is pinned' USING ERRCODE='23514'; END IF;
  IF ROW(NEW.app_id,NEW.scope,NEW.deleted_at,NEW.rootfs_path,NEW.rootfs_key,NEW.rootfs_bytes,NEW.build_id,NEW.commit_sha,NEW.environment_workload_runtime)
   IS DISTINCT FROM ROW(OLD.app_id,OLD.scope,OLD.deleted_at,OLD.rootfs_path,OLD.rootfs_key,OLD.rootfs_bytes,OLD.build_id,OLD.commit_sha,OLD.environment_workload_runtime) THEN
   RAISE EXCEPTION 'build artifact provenance is pinned' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
CREATE TRIGGER environment_build_artifact_origin_guard BEFORE DELETE OR UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION environment_build_artifact_origin_guard();

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
  NEW.status NOT IN ('live','failed') AND NOT (NEW.status='superseded' AND environment_workload_retirement_drain_authorized(OLD)) THEN
  RAISE EXCEPTION 'activated environment workload status is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL AND OLD.status='live' AND
  ROW(NEW.rootfs_path,NEW.rootfs_key,NEW.rootfs_bytes) IS DISTINCT FROM ROW(OLD.rootfs_path,OLD.rootfs_key,OLD.rootfs_bytes) THEN
  RAISE EXCEPTION 'activated environment workload artifact is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL THEN
  IF NEW.environment_workload_runtime IS DISTINCT FROM OLD.environment_workload_runtime OR
   ((OLD.environment_workload_runtime ? 'source_archive' OR OLD.environment_workload_runtime ? 'built_artifact') AND ROW(NEW.source_bytes,NEW.source_url,NEW.log_path,NEW.inferred_profile,NEW.github_source_ref,NEW.github_installation_id)
    IS DISTINCT FROM ROW(OLD.source_bytes,OLD.source_url,OLD.log_path,OLD.inferred_profile,OLD.github_source_ref,OLD.github_installation_id)) OR
   (NEW.build_id IS DISTINCT FROM OLD.build_id AND (OLD.build_id IS NOT NULL OR frozen->'source_archive' IS NULL OR
    NEW.build_id IS DISTINCT FROM (frozen->'source_archive'->>'build_id')::uuid OR NOT EXISTS(
     SELECT 1 FROM builds b WHERE b.id=NEW.build_id AND b.deployment_id=NEW.id AND b.kind=NEW.kind AND b.source_bytes=NEW.source_bytes))) OR
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
  SELECT * INTO src FROM environment_git_sources WHERE NOT detached AND id=(frozen->>'source_id')::uuid FOR UPDATE;
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
     (((src.provider='github' AND NEW.kind='github') OR (src.provider='gitlab' AND NEW.kind='tarball')) AND NEW.source_root=w.source->>'directory' AND NEW.source_url=CASE src.provider WHEN 'gitlab' THEN 'gitlab://'||src.repository_id::text||'/'||src.repository||'@'||rev.commit_sha ELSE 'github://'||src.repository||'@'||rev.commit_sha END AND environment_build_artifact_valid(NEW,false) AND frozen->'source'=w.source AND w.source_revision IS NULL AND frozen->>'definition_digest'=rev.definition_digest AND NEW.build_id IS NULL) OR
     (((src.provider='github' AND NEW.kind='github') OR (src.provider='gitlab' AND NEW.kind='tarball')) AND w.source->>'kind' IN ('source','dockerfile','function') AND NOT (w.source ? 'build_artifact_id') AND
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
      NEW.source_url=CASE src.provider WHEN 'gitlab' THEN 'gitlab://'||src.repository_id::text||'/'||src.repository||'@'||rev.commit_sha ELSE 'github://'||src.repository||'@'||rev.commit_sha END
      AND (src.provider<>'gitlab' OR (frozen->>'source_provider'='gitlab' AND frozen->>'source_repository_id'=src.repository_id::text AND frozen->>'source_repository'=src.repository)) AND NEW.build_id IS NULL AND NEW.inferred_profile IS NULL AND
      coalesce(NEW.github_source_ref,'')='' AND coalesce(NEW.image_digest,'')=''))
    AND NEW.commit_sha=rev.commit_sha AND frozen->'runtime'=coalesce(w.runtime,'{}'::jsonb) AND coalesce(frozen->'service_bindings','{}'::jsonb)=coalesce(w.service_bindings,'{}'::jsonb)
    AND coalesce(frozen->'variables','{}'::jsonb)=coalesce(w.variables,'{}'::jsonb)
    AND coalesce(frozen->'schedule','null'::jsonb)=coalesce(w.schedule,'null'::jsonb)
    AND jsonb_strip_nulls(frozen->'baseline')=jsonb_strip_nulls(a.manifest)
    AND frozen->>'start_command'=coalesce(a.start_command,'')
    AND frozen->>'app_type'=a.type AND frozen->>'runtime_base'=coalesce(a.runtime,'') AND frozen->>'workload_class'=a.workload_class
    AND coalesce(a.manifest->'env','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb) AND coalesce(a.manifest->'service_bindings','[]'::jsonb) IN ('[]'::jsonb,'null'::jsonb)
    AND environment_workload_candidate_queue_bindings_valid(frozen, NEW.app_id)
    AND environment_workload_candidate_secret_refs_valid(frozen, NEW.app_id, NEW.scope, rev.definition->'workloads'->substr(r.logical_name,10)->'secret_refs')
    AND (EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND (f.field_path IN ('source','source_revision','schedule') OR starts_with(f.field_path,'runtime/') OR starts_with(f.field_path,'service_bindings/') OR starts_with(f.field_path,'queue_bindings/') OR starts_with(f.field_path,'variables/') OR starts_with(f.field_path,'secret_refs/')))
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

CREATE OR REPLACE FUNCTION application_standard_source_runtime_producer(a apps, d deployments) RETURNS jsonb
    LANGUAGE plpgsql
    AS $_$
DECLARE f source_build_rootfs%ROWTYPE; origin build_export_publications%ROWTYPE;
 base jsonb; b base_image_producers%ROWTYPE; runtime text; kind text; borrowed_id uuid;
BEGIN
 IF d.environment_workload_runtime ? 'built_artifact' THEN
  borrowed_id:=(d.environment_workload_runtime->'built_artifact'->>'id')::uuid;
  IF NOT environment_build_artifact_valid(d,true) THEN RAISE EXCEPTION 'borrowed source runtime changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale'; END IF;
  SELECT * INTO d FROM deployments WHERE id=(d.environment_workload_runtime->'built_artifact'->>'deployment_id')::uuid FOR SHARE NOWAIT;
 END IF;
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.deployment_id=d.id FOR SHARE OF c,r NOWAIT;
 IF NOT FOUND OR (borrowed_id IS NOT NULL AND f.id IS DISTINCT FROM borrowed_id) THEN
  RAISE EXCEPTION 'source runtime selection missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 PERFORM application_standard_lock_source_runtime_rootfs(f.input_snapshot,f.id);
 SELECT * INTO origin FROM build_export_publications WHERE id=f.publication_id FOR SHARE NOWAIT;
 runtime:=CASE WHEN a.type='function' OR coalesce(a.runtime,'')<>'' THEN coalesce(nullif(a.runtime,''),d.handler,'') ELSE '' END;
 kind:=CASE WHEN a.type='function' OR coalesce(a.runtime,'')<>'' THEN 'function-layer' ELSE 'source-app-layer' END;
 IF (origin.input_snapshot->'claims'->>'account_id'=a.account_id::text AND origin.input_snapshot->'claims'->>'app_id'=a.id::text
  AND origin.input_snapshot->'claims'->>'org_id'=coalesce(a.org_id::text,'')
  AND origin.input_snapshot->'claims'->>'deployment_id'=d.id::text
  AND origin.input_snapshot->'claims'->>'runtime'=coalesce(a.runtime,'')
  AND (coalesce(d.source_sha256,'')='' OR origin.input_snapshot->'claims'->>'source_sha256'=d.source_sha256)
  AND d.kind IN ('tarball','dockerfile','github','preview')
  AND f.published_at>=origin.verified_at AND f.expires_at<=origin.expires_at
  AND f.input_snapshot->>'kind'=kind AND f.input_snapshot->>'runtime'=runtime
  AND f.input_snapshot->>'layout_version'='faas-app-layer-layout-v1'
  AND (kind<>'function-layer' OR f.input_snapshot->>'runner_digest' ~ '^sha256:[a-f0-9]{64}$')) IS NOT TRUE THEN
  RAISE EXCEPTION 'source runtime lineage changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 base:=application_standard_runtime_base_producer(f.input_snapshot);
 SELECT * INTO b FROM base_image_producers WHERE id=(base->>'producer_id')::uuid FOR SHARE NOWAIT;
 IF (base IS NOT NULL AND base->>'storage_key'=CASE WHEN runtime='' THEN 'base/base-amd64.ext4' ELSE 'base/runner-'||runtime||'-amd64.ext4' END
  AND base->>'storage_key'<>f.input_snapshot->>'storage_key' AND b.input_snapshot->>'layout_version'='faas-base-layout-v3'
  AND b.input_snapshot->>'guest_init_digest'=f.input_snapshot->>'guest_init_digest') IS NOT TRUE THEN
  RAISE EXCEPTION 'source runtime base changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 -- This historical projection grants no fresh trust. The Go native transaction
 -- authenticates the current signature and conversion intent through freshRuntimeScanTx.
 RETURN jsonb_build_object('kind',kind,'workload_name','','producer_id',f.id::text,'producer_hash',f.input_hash,
  'storage_key',f.input_snapshot->>'storage_key','digest',f.input_snapshot->>'artifact_digest','bytes',f.input_snapshot->'artifact_bytes',
  'base_producer_id',f.input_snapshot->>'base_producer_id','base_input_hash',f.input_snapshot->>'base_input_hash');
END;
$_$;

CREATE OR REPLACE FUNCTION application_standard_native_source_producer_deadline(input jsonb, artifact jsonb, now_utc timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
DECLARE f source_build_rootfs%ROWTYPE; origin build_export_publications%ROWTYPE; approval build_export_publications%ROWTYPE;
 a apps%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=(input->'artifact'->>'id')::uuid FOR UPDATE NOWAIT;
 SELECT * INTO a FROM apps WHERE id=d.app_id FOR SHARE NOWAIT;
 IF (a.id::text=input->>'app_id' AND a.account_id::text=input->>'account_id'
  AND coalesce(a.org_id::text,'')=input->>'org_id' AND d.scope=input->'artifact'->>'scope'
  AND artifact=application_standard_source_runtime_producer(a,d)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native source identity changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF d.environment_workload_runtime ? 'built_artifact' THEN
  SELECT * INTO d FROM deployments WHERE id=(d.environment_workload_runtime->'built_artifact'->>'deployment_id')::uuid FOR SHARE NOWAIT;
 END IF;
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.deployment_id=d.id FOR SHARE OF c,r NOWAIT;
 SELECT * INTO origin FROM build_export_publications WHERE id=f.publication_id FOR SHARE NOWAIT;
 SELECT * INTO approval FROM build_export_publications WHERE build_id=origin.build_id
  AND deployment_id=d.id AND app_id=a.id AND account_id=a.account_id
 ORDER BY verified_at DESC,id DESC LIMIT 1 FOR SHARE NOWAIT;
 IF (f.published_at<=now_utc AND approval.input_snapshot->'claims'=origin.input_snapshot->'claims'
  AND approval.verified_at<=now_utc AND approval.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native source approval stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 owner_inputs:=lock_build_export_publication(approval.input_snapshot,approval.input_snapshot->'proof'->>'publisher_name',true);
 IF (encode(sha256(decode(owner_inputs->>'key_der','base64')),'hex')=approval.input_snapshot->'proof'->>'publisher_key_sha256'
  AND 'sha256:'||encode(sha256(approval.payload),'hex')=approval.input_snapshot->'proof'->>'payload_digest'
  AND 'sha256:'||encode(sha256(approval.signature),'hex')=approval.input_snapshot->'proof'->>'signature_digest') IS NOT TRUE THEN
  RAISE EXCEPTION 'native source publisher changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 -- SQL fences are not ECDSA verification. Go verifies retained proof bytes in
 -- this same transaction before a native grant or publication can commit.
 RETURN approval.expires_at;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_runtime_producers(a apps, d deployments) RETURNS jsonb
    LANGUAGE plpgsql
    AS $_$
DECLARE workloads text[]; workload text; root_producer jsonb; base_producer jsonb; artifacts jsonb:='[]'::jsonb;
BEGIN
 IF NOT (coalesce(d.environment_workload_runtime ? 'built_artifact',false) OR coalesce(d.environment_workload_runtime->'source' ? 'build_artifact_id',false) OR EXISTS(SELECT 1 FROM source_build_rootfs WHERE deployment_id=d.id))
  AND NOT EXISTS(SELECT 1 FROM deployment_registry_rootfs WHERE deployment_id=d.id) THEN RETURN NULL; END IF;
 IF jsonb_typeof(d.sidecars) IS DISTINCT FROM 'array' OR EXISTS(
  SELECT 1 FROM jsonb_array_elements(d.sidecars) x GROUP BY x->>'name'
  HAVING count(*)<>1 OR coalesce(x->>'name','') !~ '^[a-z0-9][a-z0-9-]{0,62}$' OR x->>'name'='main') THEN
  RAISE EXCEPTION 'runtime workload membership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 workloads:=ARRAY[''] || ARRAY(SELECT x->>'name' FROM jsonb_array_elements(d.sidecars) x WHERE coalesce(x->>'image','')<>'' ORDER BY x->>'name');
 FOREACH workload IN ARRAY workloads LOOP
  IF workload='' AND (coalesce(d.environment_workload_runtime ? 'built_artifact',false) OR coalesce(d.environment_workload_runtime->'source' ? 'build_artifact_id',false) OR EXISTS(SELECT 1 FROM source_build_rootfs WHERE deployment_id=d.id)) THEN
   root_producer:=application_standard_source_runtime_producer(a,d);
  ELSE root_producer:=application_standard_runtime_root_producer(a,d,workload); END IF;
  artifacts:=artifacts || jsonb_build_array(root_producer);
  base_producer:=application_standard_runtime_base_producer(root_producer);
  IF base_producer IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(artifacts) x
   WHERE x->>'kind'='base-image' AND x->>'producer_id'=base_producer->>'producer_id') THEN
   artifacts:=artifacts || jsonb_build_array(base_producer);
  END IF;
 END LOOP;
 SELECT jsonb_agg(x ORDER BY CASE WHEN x->>'kind'='base-image' THEN 0 ELSE 1 END,x->>'workload_name') INTO artifacts
  FROM jsonb_array_elements(artifacts) x;
 RETURN jsonb_build_object('format','gregale.runtime-artifact-input.v1','account_id',a.account_id::text,'org_id',a.org_id::text,
  'app_id',a.id::text,'deployment_id',d.id::text,'scope',d.scope,'artifacts',artifacts);
END;
$_$;
CREATE FUNCTION environment_build_artifact_app_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='DELETE' OR NEW.status='deleted') AND EXISTS(SELECT 1 FROM source_build_rootfs f JOIN environment_build_artifact_pins p ON p.artifact_id=f.id
  WHERE f.input_snapshot->>'app_id'=OLD.id::text AND p.source_id IS NOT NULL) THEN RAISE EXCEPTION 'app has manifest-pinned build artifacts' USING ERRCODE='23514'; END IF;
 IF TG_OP='DELETE' THEN
  -- Explicit owner erasure may release same-app history only after retirement.
  -- Foreign/corrupt borrowers remain and the origin/FK guards still reject.
  IF OLD.status<>'deleted' OR EXISTS(SELECT 1 FROM instances WHERE app_id=OLD.id AND state IN ('pending','waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen','evicting_account_deleting')) THEN
   IF EXISTS(SELECT 1 FROM environment_build_artifact_pins p JOIN deployments d ON d.id=p.candidate_id WHERE d.app_id=OLD.id) THEN
    RAISE EXCEPTION 'build artifact owner has not retired' USING ERRCODE='23514';
   END IF;
  END IF;
  DELETE FROM environment_build_artifact_pins p USING deployments d,source_build_rootfs f
   WHERE p.candidate_id=d.id AND p.artifact_id=f.id AND d.app_id=OLD.id AND f.input_snapshot->>'app_id'=OLD.id::text;
  RETURN OLD;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_build_artifact_app_guard BEFORE DELETE OR UPDATE OF status ON apps FOR EACH ROW EXECUTE FUNCTION environment_build_artifact_app_guard();

END
$reuse_migration$;
-- +goose StatementEnd

-- +goose Down
-- Retained artifact references and authority guards are deliberately forward-only.
SELECT 1;
