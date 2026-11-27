-- name: EnqueueAPISandboxOperation :exec
INSERT INTO api_sandbox_provisioning (app_id, smoke_path, scenario) VALUES ($1, $2, $3);

-- name: APISandboxSlugExists :one
SELECT EXISTS (SELECT FROM apps WHERE slug = $1);

-- name: GetAPISandboxOperation :one
SELECT * FROM api_sandbox_provisioning WHERE app_id = $1;

-- name: ClaimAPISandboxOperation :one
WITH candidate AS (
    SELECT o.app_id FROM api_sandbox_provisioning o JOIN apps a ON a.id = o.app_id
    WHERE o.next_attempt_at <= sqlc.arg(claimed_at)
      AND (o.lease_until IS NULL OR o.lease_until <= sqlc.arg(claimed_at))
      AND a.status = 'active' AND a.preview_pr_state = 'open' AND a.preview_expires_at > sqlc.arg(lease_until)
    ORDER BY o.next_attempt_at, o.app_id
    LIMIT 1 FOR UPDATE OF a, o SKIP LOCKED
)
UPDATE api_sandbox_provisioning
SET lease_token = sqlc.arg(lease_token), lease_until = sqlc.arg(lease_until)
WHERE app_id = (SELECT app_id FROM candidate)
RETURNING *;

-- name: SaveAPISandboxOperation :execrows
UPDATE api_sandbox_provisioning
SET phase = $1, next_attempt_at = $2, deployment_id = $3,
    failure = $4, verification = $5, lease_token = NULL, lease_until = NULL
WHERE app_id = $6 AND lease_token = $7 AND lease_until > sqlc.arg(saved_at);

-- name: GetAPISandboxScenario :one
SELECT revision, definition, source_request_id FROM api_sandbox_scenarios WHERE deployment_id = $1;

-- name: PutAPISandboxScenario :one
INSERT INTO api_sandbox_scenarios (deployment_id, revision, definition, source_request_id)
SELECT sqlc.arg(deployment_id)::uuid, sqlc.arg(revision)::uuid, sqlc.arg(definition)::bytea, sqlc.narg(source_request_id)::uuid
WHERE sqlc.narg(expected_revision)::uuid IS NULL OR EXISTS (
    SELECT 1 FROM api_sandbox_scenarios
    WHERE deployment_id = sqlc.arg(deployment_id) AND revision = sqlc.narg(expected_revision)
)
ON CONFLICT (deployment_id) DO UPDATE
SET revision = CASE WHEN api_sandbox_scenarios.definition = excluded.definition
                        AND (excluded.source_request_id IS NULL OR excluded.source_request_id = api_sandbox_scenarios.source_request_id)
                   THEN api_sandbox_scenarios.revision ELSE excluded.revision END,
    definition = excluded.definition,
    source_request_id = COALESCE(excluded.source_request_id, api_sandbox_scenarios.source_request_id)
WHERE excluded.source_request_id IS NULL OR api_sandbox_scenarios.revision = sqlc.narg(expected_revision)
RETURNING revision, definition, source_request_id;
