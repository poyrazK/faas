-- neond's project keys (ADR-20261112160000 decision 3).

-- name: LiveProjectKey :one
SELECT key_id, sealed_key
FROM neon_project_keys
WHERE project_id = sqlc.arg(project_id) AND revoked_at IS NULL;

-- name: InsertProjectKey :exec
INSERT INTO neon_project_keys (key_id, project_id, sealed_key)
VALUES (sqlc.arg(key_id), sqlc.arg(project_id), sqlc.arg(sealed_key));

-- name: LiveProjectKeyIDs :many
SELECT key_id
FROM neon_project_keys
WHERE project_id = sqlc.arg(project_id) AND revoked_at IS NULL
ORDER BY key_id;

-- name: MarkProjectKeyRevoked :execrows
UPDATE neon_project_keys
SET revoked_at = now()
WHERE key_id = sqlc.arg(key_id) AND revoked_at IS NULL;
