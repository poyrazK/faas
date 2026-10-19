-- name: InsertProjectReleaseStage :exec
INSERT INTO project_release_stages(id,account_id,project_id,environment_slug,manifest,targets,created_at,expires_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment)::text,
 sqlc.arg(manifest)::jsonb,sqlc.arg(targets)::jsonb,sqlc.arg(created_at)::timestamptz,sqlc.arg(expires_at)::timestamptz);

-- name: ProjectReleaseStages :many
SELECT * FROM project_release_stages WHERE account_id=sqlc.arg(account_id)::uuid AND project_id=sqlc.arg(project_id)::uuid
AND environment_slug=sqlc.arg(environment)::text AND NOT cancelled AND expires_at>clock_timestamp() ORDER BY created_at DESC;

-- name: CancelProjectReleaseStage :execrows
UPDATE project_release_stages SET cancelled=true WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid
AND project_id=sqlc.arg(project_id)::uuid AND environment_slug=sqlc.arg(environment)::text;

-- name: LockProjectReleaseStageDeployments :many
SELECT id::text FROM deployments WHERE app_id=ANY(sqlc.arg(app_ids)::uuid[]) ORDER BY id FOR UPDATE;

-- name: LockProjectReleaseStagePolicies :many
SELECT p.app_id::text FROM app_binding_release_policies p WHERE p.app_id=ANY(sqlc.arg(app_ids)::uuid[])
AND p.scope=sqlc.arg(scope)::text ORDER BY p.app_id FOR SHARE;

-- name: ServiceProjectReleaseStages :many
SELECT s.* FROM project_release_stages s JOIN apps a ON a.project_id=s.project_id
WHERE a.id=sqlc.arg(app_id)::uuid AND NOT s.cancelled AND s.expires_at>clock_timestamp()
ORDER BY s.created_at;

-- name: ActiveServiceProjectReleaseTargets :many
SELECT m.deployment_id::text FROM project_release_members m JOIN project_release_sets r ON r.id=m.release_id
JOIN deployments d ON d.id=m.deployment_id
WHERE m.app_id=sqlc.arg(app_id)::uuid AND r.active AND d.environment_workload_runtime IS NULL;

-- name: ProjectReleaseStageSnapshot :one
SELECT jsonb_build_object(
 'Project',jsonb_build_object('ID',p.id,'AccountID',p.account_id,'Slug',p.slug),
 'Apps',coalesce((SELECT jsonb_agg(jsonb_build_object('ID',a.id,'AccountID',a.account_id,'ProjectID',a.project_id,'Slug',a.slug,'Status',a.status,'Manifest',a.manifest)) FROM apps a WHERE a.project_id=p.id AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')=''),'[]'::jsonb),
 'Deployments',coalesce((SELECT jsonb_object_agg(d.id::text,jsonb_build_object('ID',d.id,'AppID',d.app_id,'Scope',d.scope,'Status',d.status,'traffic_percent',d.traffic_percent,'RootfsKey',d.rootfs_key,'ImageDigest',d.image_digest,'rollout_state',d.rollout_state,'canary_total_steps',d.canary_total_steps)) FROM deployments d JOIN apps a ON a.id=d.app_id WHERE a.project_id=p.id),'{}'::jsonb),
 'Policies',coalesce((SELECT jsonb_object_agg(a.id::text,jsonb_build_object('mode',b.mode,'revision',b.revision)) FROM apps a JOIN app_binding_release_policies b ON b.app_id=a.id AND b.scope=sqlc.arg(scope)::text WHERE a.project_id=p.id),'{}'::jsonb),
 'Managed',coalesce((SELECT jsonb_object_agg(a.id::text,true) FROM apps a WHERE a.project_id=p.id AND EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=sqlc.arg(scope)::text AND (d.environment_workload_runtime IS NOT NULL OR d.environment_workload_held))),'{}'::jsonb),
 'Mirrors',coalesce((SELECT jsonb_object_agg(r.mirror_deployment_id::text,true) FROM mirror_rules r JOIN apps a ON a.id=r.app_id WHERE a.project_id=p.id),'{}'::jsonb),
 'Active',coalesce((SELECT r.id::text FROM project_release_sets r WHERE r.project_id=p.id AND r.environment_slug=sqlc.arg(scope)::text AND r.active),''))::jsonb AS snapshot
FROM projects p JOIN project_environments e ON e.project_id=p.id AND e.slug=sqlc.arg(scope)::text
WHERE p.id=sqlc.arg(project_id)::uuid AND p.account_id=sqlc.arg(account_id)::uuid;

-- name: ProjectReleaseStageByID :one
SELECT * FROM project_release_stages WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid
AND project_id=sqlc.arg(project_id)::uuid AND environment_slug=sqlc.arg(environment)::text;
