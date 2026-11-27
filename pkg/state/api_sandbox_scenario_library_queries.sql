-- name: LockAPISandboxScenarioLibrary :one
SELECT id FROM apps WHERE id = $1 AND status <> 'deleted' FOR NO KEY UPDATE;

-- name: ListAPISandboxLibraryScenarios :many
SELECT id, name, revision FROM app_api_sandbox_scenarios
WHERE app_id = $1
ORDER BY name, id;

-- name: GetAPISandboxLibraryScenario :one
SELECT * FROM app_api_sandbox_scenarios WHERE app_id = $1 AND id = $2;

-- name: GetAPISandboxLibraryScenarioByRevision :one
SELECT * FROM app_api_sandbox_scenarios WHERE app_id = $1 AND revision = $2;

-- name: CountAPISandboxLibraryScenarios :one
SELECT count(*) FROM app_api_sandbox_scenarios WHERE app_id = $1;

-- name: PutAPISandboxLibraryScenario :one
INSERT INTO app_api_sandbox_scenarios (app_id, id, revision, name, definition, source_request_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (app_id, id) DO UPDATE
SET revision = excluded.revision, name = excluded.name, definition = excluded.definition,
    source_request_id = excluded.source_request_id
RETURNING *;

-- name: DeleteAPISandboxLibraryScenario :execrows
DELETE FROM app_api_sandbox_scenarios WHERE app_id = $1 AND id = $2 AND revision = $3;
