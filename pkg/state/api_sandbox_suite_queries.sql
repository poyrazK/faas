-- name: GetAPISandboxSuite :one
SELECT revision, scenario_ids, run_before_approval FROM api_sandbox_suites WHERE app_id = $1;

-- name: PutAPISandboxSuite :exec
INSERT INTO api_sandbox_suites (app_id, revision, scenario_ids, run_before_approval) VALUES ($1, $2, $3, $4)
ON CONFLICT (app_id) DO UPDATE SET revision = excluded.revision, scenario_ids = excluded.scenario_ids,
    run_before_approval = excluded.run_before_approval;

-- name: APISandboxSuiteLibraryEntries :many
SELECT lib.id, lib.revision, lib.name, lib.definition
FROM unnest(sqlc.arg(scenario_ids)::uuid[]) WITH ORDINALITY AS selected(id, position)
JOIN app_api_sandbox_scenarios lib ON lib.app_id = sqlc.arg(app_id) AND lib.id = selected.id
ORDER BY selected.position;

-- name: CreateAPISandboxSuiteRun :one
INSERT INTO api_sandbox_suite_runs (id, app_id, suite_revision, baseline_deployment_id, candidate_deployment_id,
    baseline_artifact_pin, candidate_artifact_pin, scenario_count, created_at, deadline_at, next_attempt_at, automatic)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9, $11)
RETURNING *;

-- name: CreateAPISandboxSuiteScenario :exec
INSERT INTO api_sandbox_suite_scenarios (suite_run_id, position, scenario_id, scenario_revision, scenario_name, definition, comparison_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetAPISandboxSuiteRun :one
SELECT * FROM api_sandbox_suite_runs WHERE id = $1;

-- name: ListAPISandboxSuiteRuns :many
SELECT * FROM api_sandbox_suite_runs WHERE candidate_deployment_id = $1
ORDER BY created_at DESC, id DESC LIMIT 12;

-- name: ListAPISandboxSuiteScenarios :many
SELECT position, scenario_id, scenario_revision, scenario_name, comparison_id
FROM api_sandbox_suite_scenarios WHERE suite_run_id = $1 ORDER BY position;

-- name: GetAPISandboxSuiteScenario :one
SELECT * FROM api_sandbox_suite_scenarios WHERE suite_run_id = $1 AND position = $2;

-- name: ClaimAPISandboxSuiteRun :one
WITH due AS (
    SELECT queued.id FROM api_sandbox_suite_runs queued
    WHERE queued.finished_at IS NULL AND queued.next_attempt_at <= sqlc.arg(claimed_at)
      AND (queued.lease_until IS NULL OR queued.lease_until <= sqlc.arg(claimed_at))
    ORDER BY queued.next_attempt_at, queued.id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE api_sandbox_suite_runs r
SET lease_token = sqlc.arg(token), lease_until = sqlc.arg(expires_at)
FROM due WHERE r.id = due.id
RETURNING r.*;

-- name: CheckpointAPISandboxSuiteRun :execrows
UPDATE api_sandbox_suite_runs
SET completed_count = sqlc.arg(completed_count), next_attempt_at = sqlc.narg(next_attempt_at),
    finished_at = sqlc.narg(finished_at), failure_code = sqlc.arg(failure_code),
    waiting_for_capacity = sqlc.arg(waiting_for_capacity),
    lease_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(token) AND lease_until > sqlc.arg(saved_at)
    AND finished_at IS NULL;

-- name: EnqueueAPISandboxSuiteAutomatic :exec
INSERT INTO api_sandbox_suite_auto_requests (deployment_id, run_id, next_attempt_at)
SELECT d.id, sqlc.arg(run_id), sqlc.arg(now_at)
FROM deployments d JOIN api_sandbox_suites s ON s.app_id = d.app_id
LEFT JOIN app_release_approval_policies policy ON policy.app_id = d.app_id
WHERE d.id = sqlc.arg(deployment_id) AND d.status = 'snapshotting'
  AND cardinality(s.scenario_ids) > 0
  AND (s.run_before_approval OR policy.require_passing_sandbox_suite)
ON CONFLICT (deployment_id) DO UPDATE SET status = 'queued', next_attempt_at = excluded.next_attempt_at
WHERE api_sandbox_suite_auto_requests.status = 'disabled';

-- name: ClaimAPISandboxSuiteAutomatic :one
WITH due AS (
    SELECT queued.deployment_id FROM api_sandbox_suite_auto_requests queued
    WHERE queued.next_attempt_at <= sqlc.arg(now_at)
    ORDER BY queued.next_attempt_at, queued.deployment_id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE api_sandbox_suite_auto_requests request
SET next_attempt_at = sqlc.arg(claim_until)
FROM due WHERE request.deployment_id = due.deployment_id
RETURNING request.deployment_id, request.run_id, request.next_attempt_at;

-- name: CheckpointAPISandboxSuiteAutomatic :execrows
UPDATE api_sandbox_suite_auto_requests
SET status = sqlc.arg(status), next_attempt_at = sqlc.narg(next_attempt_at)
WHERE deployment_id = sqlc.arg(deployment_id) AND next_attempt_at = sqlc.arg(claim_until);

-- name: GetAPISandboxSuiteAutomaticStatus :one
SELECT status FROM api_sandbox_suite_auto_requests WHERE deployment_id = $1;

-- name: LockAPISandboxReleaseProject :one
SELECT p.id FROM projects p JOIN apps a ON a.project_id = p.id
WHERE a.id = $1 FOR SHARE OF p;

-- name: LockAPISandboxReleaseDeployments :many
SELECT id FROM deployments WHERE app_id = $1 AND (status = 'live' OR id = $2)
ORDER BY id FOR SHARE;

-- name: LockAPISandboxReleaseApprovalPolicy :one
SELECT require_passing_sandbox_suite FROM app_release_approval_policies WHERE app_id = $1 FOR SHARE;

-- name: GetAPISandboxReleaseAdmission :one
SELECT suite_run_id FROM api_sandbox_release_admissions WHERE deployment_id = $1;

-- name: RecordAPISandboxReleaseAdmission :exec
INSERT INTO api_sandbox_release_admissions (deployment_id, task_id, suite_run_id) VALUES ($1, $2, $3);

-- name: GetLatestAPISandboxSuiteRun :one
SELECT * FROM api_sandbox_suite_runs WHERE candidate_deployment_id = $1
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: APISandboxSuiteRunPassed :one
SELECT count(*) = sqlc.arg(scenario_count)::bigint AND coalesce(bool_and(
    e.baseline_artifact_pin = sqlc.arg(baseline_pin) AND e.candidate_artifact_pin = sqlc.arg(candidate_pin)
    AND baseline.source_deployment_id = sqlc.arg(baseline_id)
    AND candidate.source_deployment_id = sqlc.arg(candidate_id)
    AND baseline.scenario_revision = item.scenario_revision AND candidate.scenario_revision = item.scenario_revision
    AND baseline.status = 'passed' AND candidate.status = 'passed'
), false)
FROM api_sandbox_suite_scenarios item
JOIN api_sandbox_approval_checks e ON e.comparison_id = item.comparison_id
JOIN api_sandbox_comparisons comparison ON comparison.id = e.comparison_id
JOIN api_sandbox_runs baseline ON baseline.id = comparison.baseline_run_id
JOIN api_sandbox_runs candidate ON candidate.id = comparison.candidate_run_id
WHERE item.suite_run_id = sqlc.arg(run_id);
