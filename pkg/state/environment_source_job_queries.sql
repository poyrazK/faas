-- name: BindEnvironmentSourceJobArtifact :execrows
UPDATE jobs j SET image_resolved_digest='gregale-deployment:' || d.id::text,
    image_storage_key=d.rootfs_key,image_materialization_status='ready',image_materialization_error=NULL,
    image_materialized_at=clock_timestamp(),image_materialization_next_attempt_at=NULL,
    image_materialization_lease_owner=NULL,image_materialization_lease_until=NULL,
    schedule_revision=CASE WHEN j.image_resolved_digest IS DISTINCT FROM 'gregale-deployment:' || d.id::text
        OR j.image_storage_key IS DISTINCT FROM d.rootfs_key THEN j.schedule_revision+1 ELSE j.schedule_revision END,
    last_scheduled_at=CASE WHEN j.image_resolved_digest IS DISTINCT FROM 'gregale-deployment:' || d.id::text
        OR j.image_storage_key IS DISTINCT FROM d.rootfs_key THEN NULL ELSE j.last_scheduled_at END,
    updated_at=clock_timestamp()
FROM deployments d,environment_workload_graphs g
WHERE j.id=sqlc.arg(job_id)::uuid AND d.id=sqlc.arg(deployment_id)::uuid AND g.id=sqlc.arg(graph_id)::uuid
    AND j.status='paused' AND d.status='snapshotting' AND d.environment_workload_held
    AND g.phase='prepared' AND environment_workload_source_job_deployment_matches(j.id,d.id)
    -- Binding is a write: retain the provenance locks through activation.
    AND (NOT (d.environment_workload_runtime ? 'built_artifact') OR environment_build_artifact_valid(d,true))
    AND d.environment_workload_runtime->>'source_id'=g.source_id::text
    AND d.environment_workload_runtime->>'revision_id'=g.revision_id::text
    AND d.environment_workload_runtime->>'generation'=g.generation::text
    AND d.environment_workload_runtime->>'intent_version'=g.intent_version::text
    AND d.environment_workload_runtime->>'plan_hash'=g.plan_hash
    AND EXISTS (SELECT 1 FROM jsonb_array_elements(g.members) m WHERE m->>'job_id'=j.id::text
        AND m->>'app_id'=d.app_id::text AND m->>'candidate_deployment_id'=d.id::text);

-- name: EnvironmentSourceJobArtifactReady :one
SELECT EXISTS (
    SELECT 1 FROM jobs j JOIN deployments d ON j.image_resolved_digest='gregale-deployment:' || d.id::text
    JOIN environment_workload_graphs g ON g.id=sqlc.arg(graph_id)::uuid
    WHERE j.id=sqlc.arg(job_id)::uuid AND j.image_materialization_status='ready' AND j.image_storage_key=d.rootfs_key
        AND environment_workload_source_job_deployment_matches(j.id,d.id)
        AND d.environment_workload_runtime->>'source_id'=g.source_id::text
        AND EXISTS (SELECT 1 FROM jsonb_array_elements(g.members) m WHERE m->>'job_id'=j.id::text
            AND m->>'app_id'=d.app_id::text AND (m->>'candidate_deployment_id'=d.id::text OR
                (nullif(m->>'candidate_deployment_id','') IS NULL AND coalesce(m->'retained_deployments','[]'::jsonb) ? d.id::text)))
) AS ready;
