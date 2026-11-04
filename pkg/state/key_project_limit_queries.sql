-- name: InsertAPIKeyProjectLimit :exec
-- ADR-20261104085712: limits a key to projects; set once, when it is minted.
INSERT INTO api_key_project_limits (key_id, project_ids)
VALUES (sqlc.arg(key_id)::uuid, sqlc.arg(project_ids)::uuid[]);

-- name: GetAPIKeyProjectLimit :one
SELECT project_ids FROM api_key_project_limits WHERE key_id = sqlc.arg(key_id)::uuid;

-- name: SetCliAuthCodeProjects :execrows
-- Records the projects chosen for a code still waiting for approval.
UPDATE cli_auth_codes
SET project_ids = sqlc.narg(project_ids)::uuid[]
WHERE token_hash = sqlc.arg(token_hash)
  AND status = 'pending'
  AND expires_at > now();

-- name: GetCliAuthCodeProjects :one
SELECT project_ids FROM cli_auth_codes WHERE token_hash = sqlc.arg(token_hash);
