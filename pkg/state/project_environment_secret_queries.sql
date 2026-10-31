-- ADR-1139: secrets shared by every service of a project environment.

-- name: LockProjectEnvironmentForSecrets :one
-- Serializes writers of one environment's secret set so the count limit holds.
SELECT e.id FROM project_environments e
WHERE e.account_id = sqlc.arg(account_id)::uuid AND e.project_id = sqlc.arg(project_id)::uuid
  AND e.slug = sqlc.arg(environment_slug)::text
FOR UPDATE;

-- name: ProjectEnvironmentIDForSecrets :one
SELECT e.id FROM project_environments e
WHERE e.account_id = sqlc.arg(account_id)::uuid AND e.project_id = sqlc.arg(project_id)::uuid
  AND e.slug = sqlc.arg(environment_slug)::text;

-- name: CountProjectEnvironmentSecrets :one
-- The environment's key count and whether the written key is already one of them.
SELECT count(*)::bigint AS keys, coalesce(bool_or(key = sqlc.arg(key)::text), false)::bool AS exists
FROM project_environment_secrets
WHERE project_id = sqlc.arg(project_id)::uuid AND environment_slug = sqlc.arg(environment_slug)::text;

-- name: UpsertProjectEnvironmentSecret :one
INSERT INTO project_environment_secrets (account_id, project_id, environment_slug, key, ciphertext, kid, value_hash)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(environment_slug)::text, sqlc.arg(key)::text,
        sqlc.arg(ciphertext)::bytea, sqlc.arg(kid)::text, sqlc.arg(value_hash)::text)
ON CONFLICT (project_id, environment_slug, key) DO UPDATE
    SET ciphertext = excluded.ciphertext, kid = excluded.kid, value_hash = excluded.value_hash,
        secret_version = project_environment_secrets.secret_version + 1, updated_at = now()
    WHERE project_environment_secrets.account_id = excluded.account_id
RETURNING account_id, project_id, environment_slug, key, kid, value_hash, secret_version, created_at, updated_at;

-- name: DeleteProjectEnvironmentSecret :execrows
DELETE FROM project_environment_secrets
WHERE account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND environment_slug = sqlc.arg(environment_slug)::text AND key = sqlc.arg(key)::text;

-- name: ListProjectEnvironmentSecrets :many
-- Metadata only: listings never carry ciphertext.
SELECT account_id, project_id, environment_slug, key, kid, value_hash, secret_version, created_at, updated_at
FROM project_environment_secrets
WHERE account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND environment_slug = sqlc.arg(environment_slug)::text
ORDER BY key;

-- name: CopyProjectEnvironmentSecrets :execrows
-- An environment clone copies the source environment's shared secrets as it
-- copies app secrets: the envelope is not bound to an environment.
INSERT INTO project_environment_secrets (account_id, project_id, environment_slug, key, ciphertext, kid, value_hash, secret_version)
SELECT s.account_id, s.project_id, sqlc.arg(target_slug)::text, s.key, s.ciphertext, s.kid, s.value_hash, s.secret_version
FROM project_environment_secrets s
WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.project_id = sqlc.arg(project_id)::uuid
  AND s.environment_slug = sqlc.arg(source_slug)::text;

-- name: CompareAndSwapProjectSecretEnvelope :execrows
UPDATE project_environment_secrets p SET kid = sqlc.arg(replacement)::jsonb#>>'{0,kid}',
 ciphertext = decode(sqlc.arg(replacement)::jsonb#>>'{0,ciphertext}', 'base64')
WHERE p.account_id = split_part(sqlc.arg(document_id)::text, '/', 1)::uuid
 AND p.project_id = split_part(sqlc.arg(document_id)::text, '/', 2)::uuid
 AND p.environment_slug = split_part(sqlc.arg(document_id)::text, '/', 3)
 AND p.key = split_part(sqlc.arg(document_id)::text, '/', 4)
 AND release_secret_project_document(p) = sqlc.arg(previous)::jsonb;
