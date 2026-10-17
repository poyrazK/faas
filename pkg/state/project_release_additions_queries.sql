-- name: ListProjectReleaseAdditions :many
SELECT r.app_id::text AS app_id, r.attempt_id, r.fingerprint, r.dormant
FROM project_release_additions r JOIN apps a ON a.id = r.app_id
WHERE r.project_id = sqlc.arg(project_id)::text::uuid AND a.status <> 'deleted' ORDER BY r.app_id;

-- name: ReserveProjectReleaseAddition :exec
INSERT INTO project_release_additions(app_id, account_id, project_id, attempt_id, fingerprint)
VALUES(sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(project_id)::text::uuid,
       sqlc.arg(attempt_id)::text, sqlc.arg(fingerprint)::text)
ON CONFLICT(app_id) DO UPDATE SET attempt_id = EXCLUDED.attempt_id;

-- name: SetProjectReleaseAdditionDormant :exec
UPDATE project_release_additions SET dormant = sqlc.arg(dormant)::boolean
WHERE app_id = sqlc.arg(app_id)::text::uuid;

-- name: LockReleaseAdditionProject :one
SELECT id::text AS id FROM projects
WHERE id = sqlc.arg(project_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: LockReleaseAdditionAccount :one
SELECT plan FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: HasDormantReleaseAdditions :one
SELECT EXISTS (SELECT 1 FROM project_release_additions
JOIN apps a ON a.id = app_id
WHERE project_release_additions.project_id = sqlc.arg(project_id)::text::uuid AND dormant AND a.status <> 'deleted');

-- name: ReadDeploymentReleaseAddition :one
SELECT attempt_id, dormant FROM project_release_additions WHERE app_id = sqlc.arg(app_id)::text::uuid;

-- name: CaptureProjectReleaseAdditions :execrows
UPDATE project_submissions SET payload = jsonb_set(payload, '{apply,safe_release}', sqlc.arg(safe_release)::jsonb), updated_at = now()
WHERE account_id = sqlc.arg(account_id)::text::uuid AND attempt_id = sqlc.arg(attempt_id)::text AND status = 'processing';

-- name: ReadReleaseAdditionApp :one
SELECT id::text AS id, account_id::text AS account_id, project_id::text AS project_id,
       slug, root_dir, workload_name, workload_class, coalesce(start_command, '')::text AS start_command,
       manifest, require_authn, public_auth_mode, platform_tenant_required
FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND status <> 'deleted' FOR UPDATE;
