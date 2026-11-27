-- name: CreateAPISandboxComparison :exec
INSERT INTO api_sandbox_comparisons (id, baseline_run_id, candidate_run_id)
VALUES ($1, $2, $3);

-- name: GetAPISandboxComparison :one
SELECT c.id, c.created_at, sqlc.embed(baseline), sqlc.embed(candidate)
FROM api_sandbox_comparisons c
JOIN api_sandbox_runs baseline ON baseline.id = c.baseline_run_id
JOIN api_sandbox_runs candidate ON candidate.id = c.candidate_run_id
WHERE c.id = $1 AND NOT EXISTS (SELECT 1 FROM api_sandbox_approval_checks e WHERE e.comparison_id = c.id);

-- name: ListAPISandboxComparisons :many
SELECT c.id, c.created_at, sqlc.embed(baseline), sqlc.embed(candidate)
FROM api_sandbox_comparisons c
JOIN api_sandbox_runs baseline ON baseline.id = c.baseline_run_id
JOIN api_sandbox_runs candidate ON candidate.id = c.candidate_run_id
WHERE candidate.source_deployment_id = $1
  AND NOT EXISTS (SELECT 1 FROM api_sandbox_approval_checks e WHERE e.comparison_id = c.id)
ORDER BY c.created_at DESC, c.id DESC LIMIT 25;

-- name: ListAPISandboxComparisonBaselines :many
SELECT d.id, coalesce(d.commit_sha, '')::text AS commit_sha, d.created_at
FROM deployments d
WHERE d.app_id = sqlc.arg(app_id) AND (d.created_at, d.id) < (sqlc.arg(candidate_created_at)::timestamptz, sqlc.arg(candidate_id)::uuid)
    AND d.deleted_at IS NULL AND d.status IN ('live', 'superseded')
    AND d.rootfs_key <> '' AND d.rootfs_bytes > 0 AND cardinality(d.release_command) > 0
    AND d.sidecars IN ('[]'::jsonb, 'null'::jsonb)
ORDER BY d.created_at DESC, d.id DESC LIMIT 25;
