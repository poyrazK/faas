-- filename: 20261017081218727_environment_built_artifact_same_scope_rollback.sql
-- adr: 20261017190805
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_build_artifact_valid(d deployments, materialized boolean) RETURNS boolean LANGUAGE plpgsql AS $$
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

-- +goose StatementEnd
-- +goose Down
-- Retained rollback references must not be invalidated by reverting this adapter.
SELECT 1;
