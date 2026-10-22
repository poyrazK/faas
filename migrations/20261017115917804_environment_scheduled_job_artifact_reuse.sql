-- migration-compat: contract
-- Signed-artifact scheduled Jobs change shared provenance and Job-link guards.
-- filename: 20261017115917804_environment_scheduled_job_artifact_reuse.sql
-- adr: 20261017190808. Signed artifacts supply future scheduled runs; no execution is replayed.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $migration$
BEGIN
-- Atomic installation makes the reference helper a completion guard on ledger replay.
IF to_regprocedure(format('%I.environment_workload_source_job_reference(uuid,uuid,jsonb,text)',current_schema())) IS NOT NULL THEN RETURN; END IF;
CREATE OR REPLACE FUNCTION environment_build_artifact_matches(d deployments, materialized boolean) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
DECLARE b jsonb:=d.environment_workload_runtime->'built_artifact'; f source_build_rootfs%ROWTYPE; origin deployments%ROWTYPE; a apps%ROWTYPE; src environment_git_sources%ROWTYPE;
BEGIN
 SELECT * INTO f FROM source_build_rootfs WHERE id=(b->>'id')::uuid;
 SELECT * INTO origin FROM deployments WHERE id=f.deployment_id;
 SELECT * INTO a FROM apps WHERE id=d.app_id;
 SELECT * INTO src FROM environment_git_sources WHERE id=(d.environment_workload_runtime->>'source_id')::uuid;
 RETURN (b IS NOT NULL AND d.environment_workload_runtime->'source_archive' IS NULL
  AND d.environment_workload_runtime->'source'->>'build_artifact_id'=f.id::text AND d.environment_workload_runtime->'source'->>'build_artifact_hash'=f.input_hash
  AND EXISTS(SELECT 1 FROM source_build_rootfs_current c WHERE c.deployment_id=origin.id AND c.artifact_id=f.id)
  AND b->>'input_hash'=f.input_hash AND b->>'deployment_id'=origin.id::text AND b->>'build_id'=origin.build_id::text AND b->>'commit_sha'=origin.commit_sha
  AND b->>'digest'=f.input_snapshot->>'artifact_digest' AND b->>'storage_key'=f.input_snapshot->>'storage_key'
  AND b->>'rootfs_path'=f.input_snapshot->>'rootfs_path' AND b->'bytes'=f.input_snapshot->'content_bytes'
  AND origin.app_id=d.app_id AND origin.id<>d.id AND origin.deleted_at IS NULL AND a.status<>'deleted'
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

-- Writers keep the original provenance row locks through transaction commit.
-- Read-only status shares the identical predicate without acquiring locks.
CREATE OR REPLACE FUNCTION environment_build_artifact_valid(d deployments, materialized boolean)
RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE;
BEGIN
 SELECT * INTO f FROM source_build_rootfs WHERE id=(d.environment_workload_runtime->'built_artifact'->>'id')::uuid FOR SHARE NOWAIT;
 PERFORM 1 FROM deployments WHERE id=f.deployment_id FOR SHARE NOWAIT;
 PERFORM 1 FROM apps WHERE id=d.app_id FOR SHARE NOWAIT;
 RETURN environment_build_artifact_matches(d,materialized);
END $$;
-- Canonical source identity is disjoint from an OCI digest and from a build
-- commit. It binds the reviewed immutable producer ID and input hash together.
CREATE OR REPLACE FUNCTION environment_workload_source_job_reference(environment uuid,app uuid,source jsonb,revision text)
RETURNS text LANGUAGE sql IMMUTABLE AS $$
SELECT CASE WHEN environment IS NULL OR app IS NULL OR
    environment='00000000-0000-0000-0000-000000000000'::uuid OR app='00000000-0000-0000-0000-000000000000'::uuid OR
    coalesce(source->>'kind','') NOT IN ('source','dockerfile') THEN NULL
 WHEN source ? 'build_artifact_id' OR source ? 'build_artifact_hash' THEN
  CASE WHEN revision IS NULL
    AND source->>'build_artifact_id' ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'
    AND source->>'build_artifact_id'<>'00000000-0000-0000-0000-000000000000'
    AND source->>'build_artifact_hash' ~ '^[a-f0-9]{64}$'
   THEN 'gregale-source://' || environment::text || '/' || app::text || '/artifact/' || (source->>'build_artifact_id') || '/' || (source->>'build_artifact_hash') END
 WHEN revision ~ '^([a-f0-9]{40}|[a-f0-9]{64})$'
  THEN 'gregale-source://' || environment::text || '/' || app::text || '/' || revision END
$$;
CREATE OR REPLACE FUNCTION environment_workload_source_job_deployment_matches(target_job uuid,target_deployment uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1 FROM jobs j
    JOIN app_environment_workload_intents i ON i.job_id=j.id AND i.account_id=j.account_id
    JOIN active_environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
    JOIN project_environments e ON e.id=i.environment_id
    JOIN deployments d ON d.id=target_deployment AND d.app_id=i.app_id AND d.scope=e.slug
    WHERE j.id=target_job AND i.source->>'kind' IN ('source','dockerfile')
        AND j.image_ref=environment_workload_source_job_reference(i.environment_id,i.app_id,i.source,i.source_revision)
        AND d.status IN ('snapshotting','live') AND d.rootfs_key IS NOT NULL AND d.rootfs_key<>'' AND d.rootfs_bytes>0
        AND d.environment_workload_runtime->'source'=i.source
        AND d.environment_workload_runtime->'runtime'->'entrypoint'=i.runtime->'entrypoint'
        AND jsonb_typeof(i.runtime->'entrypoint')='array' AND jsonb_array_length(i.runtime->'entrypoint') BETWEEN 1 AND 64
        AND to_jsonb(j.command)=i.runtime->'entrypoint'
        AND d.environment_workload_runtime->>'source_id'=s.id::text
        AND d.environment_workload_runtime->>'environment_id'=i.environment_id::text
        AND d.environment_workload_runtime->>'app_id'=i.app_id::text
        AND (
          (NOT (i.source ? 'build_artifact_id' OR i.source ? 'build_artifact_hash')
           AND d.commit_sha=i.source_revision
           AND d.environment_workload_runtime->'built_artifact' IS NULL
           AND d.environment_workload_runtime->'source_archive'->>'commit_sha'=i.source_revision
           AND d.environment_workload_runtime->'source_archive'->>'build_id'=d.build_id::text
           AND d.environment_workload_runtime->'source_archive'->>'sha256'=d.source_sha256
           AND d.environment_workload_runtime->'source_archive'->>'path'=d.source_path)
          OR (i.source_revision IS NULL AND i.source ? 'build_artifact_id'
              AND environment_build_artifact_matches(d,true))
        )
        AND d.environment_workload_runtime->>'workload_class'='job'
)
$$;
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
              (NEW.source->>'kind' IN ('source','dockerfile')
               AND j.image_ref=environment_workload_source_job_reference(NEW.environment_id,NEW.app_id,NEW.source,NEW.source_revision))
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


END $migration$;
-- +goose StatementEnd

-- +goose Down
-- Retained and active scheduled Jobs must keep their immutable artifact adapter.
SELECT 1;
