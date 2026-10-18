-- migration-compat: contract
-- Source-Job qualification and reserved-VM guards require the coordinated GitOps rollout.
-- Source-backed schedules reuse a qualified deployment layer; they never
-- ask imaged to pull the internal source reference or rebuild per occurrence.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Preserve historical v2 receipts; only the current v3 policy can qualify a
-- candidate in the Go/runtime guards. Source secret-reference evidence uses v3.
ALTER TABLE environment_qualification_job_smoke_receipts DROP CONSTRAINT environment_qualification_job_smoke_receipts_policy_id_check;
ALTER TABLE environment_qualification_job_smoke_receipts ADD CONSTRAINT environment_qualification_job_smoke_receipts_policy_id_check CHECK (policy_id IN ('job-exit-v2','job-exit-v3'));
-- Native Job qualification now reserves a VM just like every other graph
-- member. Enforce the current shape on new writes without discarding legacy
-- Job claims that predate VM-backed qualification. Those claims cannot satisfy
-- current execution evidence; an updated claim must acquire its reserved VM.
ALTER TABLE environment_workload_qualification_requests DROP CONSTRAINT environment_workload_qualification_reserved_instance_shape;
ALTER TABLE environment_workload_qualification_requests ADD CONSTRAINT environment_workload_qualification_reserved_instance_shape
 CHECK ((phase='queued' AND reserved_instance_id IS NULL) OR (phase='claimed' AND reserved_instance_id IS NOT NULL)) NOT VALID;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM environment_workload_qualification_requests
        WHERE NOT ((phase='queued' AND reserved_instance_id IS NULL) OR (phase='claimed' AND reserved_instance_id IS NOT NULL))) THEN
        ALTER TABLE environment_workload_qualification_requests VALIDATE CONSTRAINT environment_workload_qualification_reserved_instance_shape;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $preserve_signed_jobs$
BEGIN
 -- Migration 20261017115917804 installs signed-artifact Job support atomically;
 -- its reference helper marks completion. Replaying this older migration
 -- must preserve the later provenance predicate and Job-link fences.
 IF to_regprocedure(format('%I.environment_workload_source_job_reference(uuid,uuid,jsonb,text)',current_schema())) IS NOT NULL THEN RETURN; END IF;
CREATE OR REPLACE FUNCTION environment_workload_source_job_deployment_matches(target_job uuid,target_deployment uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1 FROM jobs j
    JOIN app_environment_workload_intents i ON i.job_id=j.id AND i.account_id=j.account_id
    JOIN active_environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
    JOIN project_environments e ON e.id=i.environment_id
    JOIN deployments d ON d.id=target_deployment AND d.app_id=i.app_id AND d.scope=e.slug
    WHERE j.id=target_job AND i.source->>'kind' IN ('source','dockerfile')
        AND i.source_revision ~ '^[a-f0-9]{40}$' AND d.commit_sha=i.source_revision
        AND j.image_ref='gregale-source://' || i.environment_id::text || '/' || i.app_id::text || '/' || i.source_revision
        AND d.status IN ('snapshotting','live') AND d.rootfs_key IS NOT NULL AND d.rootfs_key<>'' AND d.rootfs_bytes>0
        AND d.environment_workload_runtime->'source'=i.source
        AND d.environment_workload_runtime->'runtime'->'entrypoint'=i.runtime->'entrypoint'
        AND jsonb_typeof(i.runtime->'entrypoint')='array' AND jsonb_array_length(i.runtime->'entrypoint') BETWEEN 1 AND 64
        AND to_jsonb(j.command)=i.runtime->'entrypoint'
        AND d.environment_workload_runtime->>'source_id'=s.id::text
        AND d.environment_workload_runtime->>'environment_id'=i.environment_id::text
        AND d.environment_workload_runtime->>'app_id'=i.app_id::text
        AND d.environment_workload_runtime->'source_archive'->>'commit_sha'=i.source_revision
        AND d.environment_workload_runtime->'source_archive'->>'build_id'=d.build_id::text
        AND d.environment_workload_runtime->'source_archive'->>'sha256'=d.source_sha256
        AND d.environment_workload_runtime->'source_archive'->>'path'=d.source_path
        AND d.environment_workload_runtime->>'workload_class'='job'
)
$$;
END
$preserve_signed_jobs$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $preserve_signed_jobs$
BEGIN
 -- Migration 20261017115917804 installs signed-artifact Job support atomically;
 -- its reference helper marks completion. Replaying this older migration
 -- must preserve the later provenance predicate and Job-link fences.
 IF to_regprocedure(format('%I.environment_workload_source_job_reference(uuid,uuid,jsonb,text)',current_schema())) IS NOT NULL THEN RETURN; END IF;
CREATE OR REPLACE FUNCTION guard_environment_workload_job_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP<>'DELETE' AND NEW.job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM jobs j
        WHERE j.id=NEW.job_id AND j.account_id=NEW.account_id
          AND j.kind='recurring' AND j.status IN ('paused','active')
          AND NEW.schedule IS NOT NULL AND (
              (NEW.source->>'kind'='image' AND j.image_ref=NEW.source->>'image') OR
              (NEW.source->>'kind' IN ('source','dockerfile') AND NEW.source_revision ~ '^[a-f0-9]{40}$'
               AND j.image_ref='gregale-source://' || NEW.environment_id::text || '/' || NEW.app_id::text || '/' || NEW.source_revision)
          )
          AND to_jsonb(j.command)=coalesce(nullif(NEW.runtime->'entrypoint','null'::jsonb),'[]'::jsonb)
          AND j.env_overrides=NEW.variables
          AND j.cron_schedule=NEW.schedule->>'cron'
          AND j.cron_timezone=NEW.schedule->>'timezone'
          AND j.schedule_policy IS NOT DISTINCT FROM NULLIF(NEW.schedule->'schedule_policy','null'::jsonb)
          AND j.failure_rules IS NOT DISTINCT FROM NULLIF(NEW.schedule->'failure_rules','null'::jsonb)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_link_contract',
            MESSAGE='GitOps Job binding does not match the reviewed source and schedule';
    END IF;
    RETURN NEW;
END;
$$;
END
$preserve_signed_jobs$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_scheduled_job_acks(target_graph uuid, target_release uuid)
RETURNS TABLE(app_id text,job_id text,job_run_id text,occurrence_id text,acknowledged_at timestamptz,service_bindings jsonb)
LANGUAGE sql STABLE AS $$
SELECT member.value->>'app_id',managed_job.id::text,proof.run_id::text,proof.occurrence_id::text,proof.finished_at,
    coalesce(intent.service_bindings,'{}'::jsonb)
FROM environment_workload_graphs graph
JOIN active_environment_git_sources source ON source.id=graph.source_id
JOIN project_environments environment ON environment.id=source.environment_id
JOIN project_release_sets release ON release.id=target_release AND release.project_id=source.project_id
CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS member(value)
JOIN app_environment_workload_intents intent ON intent.account_id=source.account_id
    AND intent.environment_id=source.environment_id AND intent.app_id::text=member.value->>'app_id'
    AND intent.job_id::text=member.value->>'job_id'
JOIN jobs managed_job ON managed_job.id=intent.job_id
JOIN project_release_members release_member ON release_member.release_id=release.id
    AND release_member.app_id=intent.app_id
JOIN deployments deployment ON deployment.id=release_member.deployment_id
    AND deployment.app_id=release_member.app_id AND deployment.status='live'
    AND NOT deployment.environment_workload_held AND deployment.scope=environment.slug
JOIN LATERAL (
    SELECT occurrence.id AS occurrence_id,run.id AS run_id,occurrence.finished_at
    FROM schedule_occurrences occurrence
    JOIN job_runs run ON run.id=occurrence.job_run_id AND run.job_id=managed_job.id
    WHERE occurrence.job_id=managed_job.id
        AND occurrence.schedule_revision=managed_job.schedule_revision
        AND occurrence.status='succeeded' AND occurrence.finished_at IS NOT NULL
        AND occurrence.created_at>=release.created_at
        AND occurrence.started_at IS NOT NULL AND occurrence.started_at>=release.created_at
        AND run.occurrence_id=occurrence.id AND run.trigger_kind='scheduled'
        AND run.aggregate_status='succeeded' AND run.finished_at IS NOT NULL
        AND run.created_at>=release.created_at AND run.started_at IS NOT NULL AND run.started_at>=release.created_at
        AND run.finished_at>=release.created_at
        AND occurrence.finished_at>=run.finished_at
        AND run.image_ref_snapshot=managed_job.image_ref
        AND run.image_resolved_digest_snapshot=managed_job.image_resolved_digest
        AND run.image_storage_key_snapshot=managed_job.image_storage_key
        AND run.retry_max=managed_job.retry_max
        AND run.task_timeout_s=managed_job.task_timeout_s
        AND run.parallelism=managed_job.max_parallelism
        AND run.command=managed_job.command
        AND run.ram_mb_snapshot=managed_job.ram_mb
        AND run.env_overrides=managed_job.env_overrides
        AND run.effective_env_snapshot=managed_job.env_overrides || run.env_overrides
        AND run.failure_rules IS NOT DISTINCT FROM managed_job.failure_rules
        AND run.execution_class='standard' AND run.failure_policy='continue' AND run.tasks=1
        AND run.input_manifest_version=0 AND coalesce(run.input_digest,'')=''
        AND run.input_manifest_uri IS NULL AND run.input_manifest_sha256 IS NULL
    ORDER BY occurrence.finished_at,occurrence.id
    LIMIT 1
) proof ON true
WHERE graph.id=target_graph AND graph.phase='prepared'
    AND source.mode='enforce' AND NOT source.suspended
    AND source.generation=graph.generation AND source.intent_version=graph.intent_version
    AND source.approved_revision_id=graph.revision_id
    AND release.active AND release.environment_slug=environment.slug
    AND member.value->>'execution_mode'='job'
    AND member.value->>'schedule_configured'='true'
    AND member.value->>'job_smoke_configured'='true'
    AND managed_job.id::text=member.value->>'job_id'
    AND managed_job.account_id=source.account_id AND managed_job.kind='recurring' AND managed_job.status='active'
    AND managed_job.image_materialization_status='ready'
    AND coalesce(managed_job.image_storage_key,'')<>''
    AND (
        (intent.source->>'kind'='image' AND managed_job.image_ref=intent.source->>'image'
         AND managed_job.image_resolved_digest=substring(intent.source->>'image' FROM '@(sha256:[a-f0-9]{64})$')) OR
        (environment_workload_source_job_deployment_matches(managed_job.id,deployment.id)
         AND managed_job.image_resolved_digest='gregale-deployment:' || deployment.id::text
         AND managed_job.image_storage_key=deployment.rootfs_key)
    )
    AND intent.schedule IS NOT NULL AND managed_job.cron_schedule=intent.schedule->>'cron'
    AND managed_job.cron_timezone=intent.schedule->>'timezone'
    AND managed_job.schedule_policy IS NOT DISTINCT FROM NULLIF(intent.schedule->'schedule_policy','null'::jsonb)
    AND managed_job.failure_rules IS NOT DISTINCT FROM NULLIF(intent.schedule->'failure_rules','null'::jsonb)
    AND managed_job.max_parallelism=1 AND managed_job.retry_max=0
    AND managed_job.ram_mb>0 AND managed_job.task_timeout_s>0
    AND to_jsonb(managed_job.command)=coalesce(nullif(intent.runtime->'entrypoint','null'::jsonb),'[]'::jsonb)
    AND managed_job.env_overrides=coalesce(nullif(member.value->'variables','null'::jsonb),'{}'::jsonb)
    AND coalesce(nullif(intent.variables,'null'::jsonb),'{}'::jsonb)=coalesce(nullif(member.value->'variables','null'::jsonb),'{}'::jsonb)
    AND coalesce(nullif(member.value->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(intent.service_bindings,'{}'::jsonb)
    AND coalesce(member.value->>'service_bindings_configured','false')=(coalesce(intent.service_bindings,'{}'::jsonb)<>'{}'::jsonb)::text
    AND coalesce(member.value->>'queue_bindings_configured','false')='false'
    AND coalesce(nullif(member.value->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb
    AND (member.value->>'candidate_deployment_id'=deployment.id::text OR
        (nullif(member.value->>'candidate_deployment_id','') IS NULL
            AND coalesce(member.value->'retained_deployments','[]'::jsonb) ? deployment.id::text))
$$;


-- +goose StatementEnd
-- Operational OCI materializer APIs cannot replace a source Job's layer.
-- The controller lease must own both artifact publication and activation.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_source_job_artifact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.image_ref LIKE 'gregale-source://%' OR NEW.image_ref LIKE 'gregale-source://%') AND
       ROW(NEW.image_resolved_digest,NEW.image_storage_key,NEW.image_materialization_status,
           NEW.image_materialization_attempts,NEW.schedule_revision)
       IS DISTINCT FROM ROW(OLD.image_resolved_digest,OLD.image_storage_key,OLD.image_materialization_status,
           OLD.image_materialization_attempts,OLD.schedule_revision) AND NOT EXISTS (
        SELECT 1 FROM app_environment_workload_intents i
        JOIN active_environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
        JOIN environment_gitops_jobs work ON work.source_id=s.id
        WHERE i.job_id=OLD.id AND s.mode='enforce' AND NOT s.suspended
            AND work.desired_generation=s.generation AND work.claimed_generation=s.generation
            AND work.lease_until>clock_timestamp() AND work.lease_token<>''
            AND work.lease_token=current_setting('gregale.gitops_lease',true)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
            MESSAGE='Source Job artifacts require the environment controller lease';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE TRIGGER guard_environment_source_job_artifact BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION guard_environment_source_job_artifact();
-- +goose StatementEnd
-- +goose Down
ALTER TABLE environment_qualification_job_smoke_receipts DROP CONSTRAINT environment_qualification_job_smoke_receipts_policy_id_check;
ALTER TABLE environment_qualification_job_smoke_receipts ADD CONSTRAINT environment_qualification_job_smoke_receipts_policy_id_check CHECK (policy_id='job-exit-v2');
ALTER TABLE environment_workload_qualification_requests DROP CONSTRAINT environment_workload_qualification_reserved_instance_shape;
ALTER TABLE environment_workload_qualification_requests ADD CONSTRAINT environment_workload_qualification_reserved_instance_shape
 CHECK ((phase='queued' AND reserved_instance_id IS NULL) OR (phase='claimed' AND
 ((execution_mode='job' AND reserved_instance_id IS NULL) OR (execution_mode<>'job' AND reserved_instance_id IS NOT NULL))));
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM app_environment_workload_intents i JOIN jobs j ON j.id=i.job_id
        WHERE i.source->>'kind' IN ('source','dockerfile')) THEN
        RAISE EXCEPTION 'remove source-backed Job schedules before rolling back their artifact contract';
    END IF;
END $$;
DROP TRIGGER guard_environment_source_job_artifact ON jobs;
DROP FUNCTION guard_environment_source_job_artifact();
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_job_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP<>'DELETE' AND NEW.job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM jobs j
        WHERE j.id=NEW.job_id AND j.account_id=NEW.account_id
          AND j.kind='recurring' AND j.status IN ('paused','active')
          AND NEW.schedule IS NOT NULL AND NEW.source->>'kind'='image'
          AND j.image_ref=NEW.source->>'image'
          AND j.env_overrides=NEW.variables
          AND j.cron_schedule=NEW.schedule->>'cron'
          AND j.cron_timezone=NEW.schedule->>'timezone'
          AND j.schedule_policy IS NOT DISTINCT FROM NULLIF(NEW.schedule->'schedule_policy','null'::jsonb)
          AND j.failure_rules IS NOT DISTINCT FROM NULLIF(NEW.schedule->'failure_rules','null'::jsonb)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_link_contract',
            MESSAGE='GitOps Job binding does not match the reviewed image and schedule';
    END IF;
    RETURN NEW;
END;
$$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_scheduled_job_acks(target_graph uuid, target_release uuid)
RETURNS TABLE(app_id text,job_id text,job_run_id text,occurrence_id text,acknowledged_at timestamptz,service_bindings jsonb)
LANGUAGE sql STABLE AS $$
SELECT member.value->>'app_id',managed_job.id::text,proof.run_id::text,proof.occurrence_id::text,proof.finished_at,
    coalesce(intent.service_bindings,'{}'::jsonb)
FROM environment_workload_graphs graph
JOIN active_environment_git_sources source ON source.id=graph.source_id
JOIN project_environments environment ON environment.id=source.environment_id
JOIN project_release_sets release ON release.id=target_release AND release.project_id=source.project_id
CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS member(value)
JOIN app_environment_workload_intents intent ON intent.account_id=source.account_id
    AND intent.environment_id=source.environment_id AND intent.app_id::text=member.value->>'app_id'
    AND intent.job_id::text=member.value->>'job_id'
JOIN jobs managed_job ON managed_job.id=intent.job_id
JOIN project_release_members release_member ON release_member.release_id=release.id
    AND release_member.app_id=intent.app_id
JOIN deployments deployment ON deployment.id=release_member.deployment_id
    AND deployment.app_id=release_member.app_id AND deployment.status='live'
    AND NOT deployment.environment_workload_held AND deployment.scope=environment.slug
JOIN LATERAL (
    SELECT occurrence.id AS occurrence_id,run.id AS run_id,occurrence.finished_at
    FROM schedule_occurrences occurrence
    JOIN job_runs run ON run.id=occurrence.job_run_id AND run.job_id=managed_job.id
    WHERE occurrence.job_id=managed_job.id
        AND occurrence.schedule_revision=managed_job.schedule_revision
        AND occurrence.status='succeeded' AND occurrence.finished_at IS NOT NULL
        AND occurrence.created_at>=release.created_at
        AND occurrence.started_at IS NOT NULL AND occurrence.started_at>=release.created_at
        AND run.occurrence_id=occurrence.id AND run.trigger_kind='scheduled'
        AND run.aggregate_status='succeeded' AND run.finished_at IS NOT NULL
        AND run.created_at>=release.created_at AND run.started_at IS NOT NULL AND run.started_at>=release.created_at
        AND run.finished_at>=release.created_at
        AND occurrence.finished_at>=run.finished_at
        AND run.image_ref_snapshot=managed_job.image_ref
        AND run.image_resolved_digest_snapshot=managed_job.image_resolved_digest
        AND run.image_storage_key_snapshot=managed_job.image_storage_key
        AND run.retry_max=managed_job.retry_max
        AND run.task_timeout_s=managed_job.task_timeout_s
        AND run.parallelism=managed_job.max_parallelism
        AND run.command=managed_job.command
        AND run.ram_mb_snapshot=managed_job.ram_mb
        AND run.env_overrides=managed_job.env_overrides
        AND run.effective_env_snapshot=managed_job.env_overrides || run.env_overrides
        AND run.failure_rules IS NOT DISTINCT FROM managed_job.failure_rules
        AND run.execution_class='standard' AND run.failure_policy='continue' AND run.tasks=1
        AND run.input_manifest_version=0 AND coalesce(run.input_digest,'')=''
        AND run.input_manifest_uri IS NULL AND run.input_manifest_sha256 IS NULL
    ORDER BY occurrence.finished_at,occurrence.id
    LIMIT 1
) proof ON true
WHERE graph.id=target_graph AND graph.phase='prepared'
    AND source.mode='enforce' AND NOT source.suspended
    AND source.generation=graph.generation AND source.intent_version=graph.intent_version
    AND source.approved_revision_id=graph.revision_id
    AND release.active AND release.environment_slug=environment.slug
    AND member.value->>'execution_mode'='job'
    AND member.value->>'schedule_configured'='true'
    AND member.value->>'job_smoke_configured'='true'
    AND managed_job.id::text=member.value->>'job_id'
    AND managed_job.account_id=source.account_id AND managed_job.kind='recurring' AND managed_job.status='active'
    AND managed_job.image_materialization_status='ready'
    AND coalesce(managed_job.image_storage_key,'')<>''
    AND managed_job.image_resolved_digest=substring(intent.source->>'image' FROM '@(sha256:[a-f0-9]{64})$')
    AND intent.source->>'kind'='image' AND managed_job.image_ref=intent.source->>'image'
    AND intent.schedule IS NOT NULL AND managed_job.cron_schedule=intent.schedule->>'cron'
    AND managed_job.cron_timezone=intent.schedule->>'timezone'
    AND managed_job.schedule_policy IS NOT DISTINCT FROM NULLIF(intent.schedule->'schedule_policy','null'::jsonb)
    AND managed_job.failure_rules IS NOT DISTINCT FROM NULLIF(intent.schedule->'failure_rules','null'::jsonb)
    AND managed_job.max_parallelism=1 AND managed_job.retry_max=0
    AND managed_job.ram_mb>0 AND managed_job.task_timeout_s>0 AND cardinality(managed_job.command)=0
    AND managed_job.env_overrides=coalesce(nullif(member.value->'variables','null'::jsonb),'{}'::jsonb)
    AND coalesce(nullif(intent.variables,'null'::jsonb),'{}'::jsonb)=coalesce(nullif(member.value->'variables','null'::jsonb),'{}'::jsonb)
    AND coalesce(nullif(member.value->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(intent.service_bindings,'{}'::jsonb)
    AND coalesce(member.value->>'service_bindings_configured','false')=(coalesce(intent.service_bindings,'{}'::jsonb)<>'{}'::jsonb)::text
    AND coalesce(member.value->>'queue_bindings_configured','false')='false'
    AND coalesce(nullif(member.value->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb
    AND (member.value->>'candidate_deployment_id'=deployment.id::text OR
        (nullif(member.value->>'candidate_deployment_id','') IS NULL
            AND coalesce(member.value->'retained_deployments','[]'::jsonb) ? deployment.id::text))
$$;


-- +goose StatementEnd
DROP FUNCTION environment_workload_source_job_deployment_matches(uuid,uuid);
