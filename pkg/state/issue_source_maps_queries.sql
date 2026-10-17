-- name: IssueReadSourceMaps :one
SELECT * FROM issue_source_map_artifacts WHERE app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id);

-- name: IssueCountReadySourceMaps :one
SELECT count(*) FROM issue_source_map_artifacts WHERE app_id=$1 AND canonical_manifest IS NOT NULL;

-- name: IssuePublishSourceMaps :one
INSERT INTO issue_source_map_artifacts(app_id,deployment_id,manifest_sha256,canonical_manifest,map_count,created_by_account_id)
VALUES(sqlc.arg(app_id),sqlc.arg(deployment_id),sqlc.arg(manifest_sha256),sqlc.arg(canonical_manifest),sqlc.arg(map_count),sqlc.arg(actor_account_id))
ON CONFLICT(app_id,deployment_id) DO UPDATE SET canonical_manifest=EXCLUDED.canonical_manifest,retired_at=NULL,retired_by_account_id=NULL
WHERE issue_source_map_artifacts.manifest_sha256=EXCLUDED.manifest_sha256
RETURNING *;

-- name: IssueRetireSourceMaps :one
UPDATE issue_source_map_artifacts SET canonical_manifest=NULL,retired_by_account_id=CASE WHEN retired_at IS NULL THEN sqlc.arg(actor_account_id)::uuid ELSE retired_by_account_id END,
retired_at=COALESCE(retired_at,now()) WHERE app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) RETURNING *;

-- name: IssueSourceMapDeploymentOwner :one
SELECT d.id FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=sqlc.arg(deployment_id) AND d.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id);
