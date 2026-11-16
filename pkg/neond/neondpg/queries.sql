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

-- name: LiveProjectKeys :many
-- Every unrevoked key, for the backfill, rotation and orphan sweep.
SELECT key_id, project_id, created_at
FROM neon_project_keys
WHERE revoked_at IS NULL
ORDER BY project_id;

-- name: ReplaceProjectKey :execrows
-- Rotation: retire the old key and record its replacement in one statement,
-- so the project never has two live keys or none. No row: the old key was
-- not the project's live key.
WITH retired AS (
    UPDATE neon_project_keys AS k
    SET revoked_at = now()
    WHERE k.key_id = sqlc.arg(old_key_id)::bigint AND k.project_id = sqlc.arg(project_id)::text AND k.revoked_at IS NULL
    RETURNING k.project_id AS retired_project
)
INSERT INTO neon_project_keys (key_id, project_id, sealed_key)
SELECT sqlc.arg(new_key_id)::bigint, retired.retired_project, sqlc.arg(sealed_key)::bytea FROM retired;
