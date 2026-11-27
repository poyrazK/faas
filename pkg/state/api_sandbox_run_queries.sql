-- name: CreateAPISandboxRun :exec
INSERT INTO api_sandbox_runs (id, source_deployment_id, sandbox_app_id, scenario_revision, scenario_name, partner_step_count, partner_webhook)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetAPISandboxRun :one
SELECT * FROM api_sandbox_runs WHERE id = $1;

-- name: ListAPISandboxRuns :many
SELECT * FROM api_sandbox_runs WHERE source_deployment_id = $1 ORDER BY created_at DESC, id DESC LIMIT 25;

-- name: ClaimAPISandboxRun :one
WITH candidate AS (
    SELECT r.id, r.status = 'running' AS interrupted,
           (r.partner_claim IS NOT NULL)::boolean AS partner_interrupted
    FROM api_sandbox_runs r
    LEFT JOIN apps a ON a.id = r.sandbox_app_id
    LEFT JOIN api_sandbox_provisioning o ON o.app_id = r.sandbox_app_id
    LEFT JOIN api_sandbox_webhook_partners p ON p.run_id = r.id
    WHERE (r.status = 'queued' AND (
        a.id IS NULL OR a.status <> 'active' OR a.preview_pr_state IS DISTINCT FROM 'open'
        OR a.preview_expires_at <= sqlc.arg(at_time)
        OR (r.sandbox_deployment_id IS NULL AND o.phase IN ('live', 'failed'))
        OR (r.partner_step_count > 0 AND jsonb_array_length(r.steps) = r.partner_step_count)
        OR r.partner_claim_until <= sqlc.arg(at_time)
        OR (r.partner_outcome_until IS NOT NULL AND (p.outcome_at IS NOT NULL OR r.partner_outcome_until <= sqlc.arg(at_time)))
    )) OR (r.status = 'running' AND r.lease_until <= sqlc.arg(at_time))
    ORDER BY r.created_at, r.id
    LIMIT 1 FOR UPDATE OF r SKIP LOCKED
)
UPDATE api_sandbox_runs r
SET status = 'running', started_at = coalesce(r.started_at, sqlc.arg(at_time)),
    lease_token = sqlc.arg(lease_token), lease_until = sqlc.arg(lease_until),
    partner_claim = NULL, partner_claim_until = NULL
FROM candidate WHERE r.id = candidate.id
RETURNING r.*, candidate.interrupted, candidate.partner_interrupted;

-- name: AppendAPISandboxRunStep :execrows
UPDATE api_sandbox_runs
SET steps = steps || sqlc.arg(step)::jsonb, sandbox_deployment_id = sqlc.arg(deployment_id)
WHERE id = sqlc.arg(run_id) AND lease_token = sqlc.arg(lease_token) AND lease_until > sqlc.arg(at_time);

-- name: FinishAPISandboxRun :one
WITH finished AS (
    UPDATE api_sandbox_runs r
    SET status = sqlc.arg(outcome), finished_at = sqlc.arg(at_time), failure_code = sqlc.arg(failure_code),
        lease_token = NULL, lease_until = NULL
    WHERE r.id = sqlc.arg(run_id) AND r.lease_token = sqlc.arg(lease_token) AND r.lease_until > sqlc.arg(at_time)
    RETURNING r.sandbox_app_id
), closing AS (
    UPDATE apps a SET preview_pr_state = 'tearing_down'
    FROM finished WHERE a.id = finished.sandbox_app_id AND a.status = 'active' AND a.preview_pr_state = 'open'
)
SELECT EXISTS(SELECT 1 FROM finished);

-- name: ShareAPISandboxRun :execrows
UPDATE api_sandbox_runs SET share_hash = $2, share_expires_at = $3
WHERE id = $1 AND finished_at IS NOT NULL;

-- name: OpenAPISandboxPartnerRun :execrows
UPDATE api_sandbox_runs
SET status = 'queued', sandbox_deployment_id = sqlc.arg(deployment_id), lease_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(run_id) AND lease_token = sqlc.arg(lease_token) AND lease_until > sqlc.arg(at_time)
  AND partner_step_count > 0 AND sandbox_deployment_id IS NULL;

-- name: ClaimAPISandboxPartnerStep :one
UPDATE api_sandbox_runs r
SET partner_claim = sqlc.arg(claim_token), partner_claim_until = sqlc.arg(claim_until)
FROM apps a, api_sandbox_partners p, api_sandbox_provisioning o
WHERE r.sandbox_app_id = sqlc.arg(app_id) AND a.id = r.sandbox_app_id AND p.app_id = a.id AND o.app_id = a.id
  AND a.status = 'active' AND a.preview_pr_state = 'open' AND a.preview_expires_at > sqlc.arg(at_time)
  AND p.consumer_key_id = sqlc.arg(key_id) AND p.revoked_at IS NULL
  AND o.phase = 'live' AND o.deployment_id = r.sandbox_deployment_id
  AND r.status = 'queued' AND r.sandbox_deployment_id = sqlc.arg(deployment_id) AND r.partner_claim IS NULL
  AND jsonb_array_length(r.steps) < r.partner_step_count
  AND convert_from(o.scenario, 'UTF8')::jsonb->'requests'->jsonb_array_length(r.steps)->>'method' = sqlc.arg(method)::text
  AND convert_from(o.scenario, 'UTF8')::jsonb->'requests'->jsonb_array_length(r.steps)->>'path' = sqlc.arg(target)::text
RETURNING r.id, jsonb_array_length(r.steps)::integer AS step_index,
          ((convert_from(o.scenario, 'UTF8')::jsonb->'requests'->jsonb_array_length(r.steps)) - 'body' - 'assertions' - 'path')::jsonb AS example;

-- name: CompleteAPISandboxPartnerStep :one
WITH completed AS (
    UPDATE api_sandbox_runs r
    SET steps = r.steps || sqlc.arg(step)::jsonb, partner_claim = NULL, partner_claim_until = NULL,
        status = CASE WHEN sqlc.arg(failed)::boolean THEN 'failed' ELSE 'queued' END,
        finished_at = CASE WHEN sqlc.arg(failed)::boolean THEN sqlc.arg(at_time)::timestamptz ELSE NULL END,
        failure_code = sqlc.arg(failure_code)
    WHERE r.id = sqlc.arg(run_id) AND r.status = 'queued' AND r.partner_claim = sqlc.arg(claim_token)
      AND r.partner_claim_until > sqlc.arg(at_time)
    RETURNING r.sandbox_app_id
), closing AS (
    UPDATE apps a SET preview_pr_state = 'tearing_down'
    FROM completed WHERE a.id = completed.sandbox_app_id AND a.status = 'active' AND a.preview_pr_state = 'open'
      AND sqlc.arg(failed)::boolean
)
SELECT EXISTS(SELECT 1 FROM completed);
