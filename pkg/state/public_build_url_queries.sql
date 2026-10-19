-- name: SetAppPublicBuildURLs :execrows
UPDATE apps SET manifest=jsonb_set(COALESCE(manifest,'{}'::jsonb),'{public_build_urls}',$3::jsonb,true)
WHERE account_id=$1 AND id=$2 AND status<>'deleted' AND COALESCE(preview_of_slug,'')='';

-- name: GetDeploymentPublicBuildURLs :one
SELECT COALESCE(c.urls,'{}'::jsonb)::jsonb AS urls FROM deployments d
LEFT JOIN deployment_public_build_urls c ON c.deployment_id=d.id WHERE d.id=$1;

-- name: CopyDeploymentPublicBuildURLs :exec
INSERT INTO deployment_public_build_urls(deployment_id,urls)
SELECT d.id,COALESCE(c.urls,'{}'::jsonb) FROM deployments d
LEFT JOIN deployment_public_build_urls c ON c.deployment_id=sqlc.arg(source_deployment_id)::uuid
WHERE d.id=sqlc.arg(deployment_id)::uuid
ON CONFLICT(deployment_id) DO UPDATE SET urls=EXCLUDED.urls;
