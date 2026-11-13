-- name: ReadAppScalingRevision :one
SELECT scaling_revision FROM apps WHERE id = $1;

-- name: AdvanceAppScalingRevision :one
UPDATE apps SET scaling_revision = greatest(scaling_revision, $2::bigint + 1)
WHERE id = $1 RETURNING scaling_revision;

-- name: ReadAppScalingReceipt :one
SELECT request_hash, response FROM app_scaling_receipts
WHERE app_id = $1 AND scope = $2 AND request_id = $3;

-- name: InsertAppScalingReceipt :exec
INSERT INTO app_scaling_receipts (app_id, scope, request_id, request_hash, response)
VALUES ($1, $2, $3, $4, $5);

-- name: LockAppScalingEnvironment :one
SELECT e.id, e.protected FROM project_environments e
JOIN apps a ON a.project_id = e.project_id AND a.account_id = e.account_id
WHERE a.id = $1 AND a.account_id = $2 AND e.slug = $3 AND a.status <> 'deleted'
FOR UPDATE OF e;

-- name: ReadAppScalingWorkload :one
SELECT s.revision, s.settings, s.config_hash FROM project_environment_workload_specs s
JOIN project_environment_workload_heads h ON h.spec_id = s.id
JOIN project_environments e ON e.id = s.environment_id
WHERE s.app_id = $1 AND e.slug = $2;
