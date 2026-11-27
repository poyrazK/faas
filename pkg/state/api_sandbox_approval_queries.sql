-- name: CreateAPISandboxApprovalCheck :exec
INSERT INTO api_sandbox_approval_checks (comparison_id, baseline_artifact_pin, candidate_artifact_pin)
VALUES ($1, $2, $3);

-- name: GetAPISandboxApprovalCheck :one
SELECT c.id, c.created_at, e.baseline_artifact_pin, e.candidate_artifact_pin, sqlc.embed(baseline), sqlc.embed(candidate)
FROM api_sandbox_approval_checks e
JOIN api_sandbox_comparisons c ON c.id = e.comparison_id
JOIN api_sandbox_runs baseline ON baseline.id = c.baseline_run_id
JOIN api_sandbox_runs candidate ON candidate.id = c.candidate_run_id
WHERE e.comparison_id = $1;

-- name: ListAPISandboxApprovalChecks :many
SELECT c.id, c.created_at, e.baseline_artifact_pin, e.candidate_artifact_pin, sqlc.embed(baseline), sqlc.embed(candidate)
FROM api_sandbox_approval_checks e
JOIN api_sandbox_comparisons c ON c.id = e.comparison_id
JOIN api_sandbox_runs baseline ON baseline.id = c.baseline_run_id
JOIN api_sandbox_runs candidate ON candidate.id = c.candidate_run_id
WHERE candidate.source_deployment_id = $1
  AND NOT EXISTS (SELECT 1 FROM api_sandbox_suite_scenarios s WHERE s.comparison_id = e.comparison_id)
ORDER BY c.created_at DESC, c.id DESC LIMIT 25;

-- name: GetAPISandboxApprovalPinForApp :one
SELECT CASE WHEN c.candidate_run_id = r.id THEN e.candidate_artifact_pin ELSE e.baseline_artifact_pin END::text AS artifact_pin,
       (c.candidate_run_id = r.id)::boolean AS candidate
FROM api_sandbox_runs r
JOIN api_sandbox_comparisons c ON r.id IN (c.baseline_run_id, c.candidate_run_id)
JOIN api_sandbox_approval_checks e ON e.comparison_id = c.id
WHERE r.sandbox_app_id = $1;

-- name: APISandboxSourceHasReleaseTask :one
SELECT EXISTS (SELECT 1 FROM app_tasks WHERE deployment_id = $1 AND kind = 'release');
