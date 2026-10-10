-- Scheduled GitOps Jobs are opened with release publication and must complete
-- an occurrence from the active schedule before the graph is serving.
-- +goose Up
CREATE INDEX IF NOT EXISTS schedule_occurrences_succeeded_job_release_proof
    ON schedule_occurrences(job_id,schedule_revision,finished_at,id)
    WHERE job_id IS NOT NULL AND job_run_id IS NOT NULL AND status='succeeded';

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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_scheduled_jobs_ready(target_graph uuid, target_release uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT (SELECT count(*) FROM environment_workload_graphs graph
    CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS member(value)
    WHERE graph.id=target_graph AND member.value->>'execution_mode'='job') =
    (SELECT count(*) FROM environment_workload_scheduled_job_acks(target_graph,target_release))
$$;

-- +goose StatementEnd
-- Preserve the old readiness predicate under a stable name so its existing
-- route and queue checks remain intact. This catalog guard makes reapplying
-- the migration safe if its Goose ledger entry was lost.
-- +goose StatementBegin
DO $$ BEGIN
    IF to_regprocedure('environment_workload_serving_ready_without_scheduled_jobs(uuid)') IS NULL THEN
        ALTER FUNCTION environment_workload_serving_ready(uuid)
            RENAME TO environment_workload_serving_ready_without_scheduled_jobs;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_ready(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT environment_workload_serving_ready_without_scheduled_jobs(target_graph)
    AND EXISTS (
        SELECT 1 FROM environment_workload_serving_receipts receipt
        JOIN project_release_sets release ON release.id=receipt.release_set_id AND release.active
        WHERE receipt.graph_id=target_graph
            AND environment_workload_scheduled_jobs_ready(target_graph,release.id)
    )
$$;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM app_environment_workload_intents intent
        JOIN jobs managed_job ON managed_job.id=intent.job_id
        WHERE intent.schedule IS NOT NULL AND managed_job.status='active'
    ) THEN
        RAISE EXCEPTION 'active GitOps scheduled Jobs exist; pause or detach them before rolling back serving evidence';
    END IF;
END $$;
-- +goose StatementEnd

DROP FUNCTION environment_workload_serving_ready(uuid);
ALTER FUNCTION environment_workload_serving_ready_without_scheduled_jobs(uuid)
    RENAME TO environment_workload_serving_ready;
DROP FUNCTION environment_workload_scheduled_jobs_ready(uuid,uuid);
DROP FUNCTION environment_workload_scheduled_job_acks(uuid,uuid);
DROP INDEX schedule_occurrences_succeeded_job_release_proof;
