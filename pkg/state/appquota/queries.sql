-- name: ResolveOwner :one
SELECT id, plan, status, deleted_pending, personal_org, personal_owner_account_id
FROM orgs
WHERE id = sqlc.narg(org_id)::uuid
   OR (sqlc.narg(org_id)::uuid IS NULL AND personal_org AND personal_owner_account_id = sqlc.arg(account_id)::uuid)
FOR SHARE;

-- name: LockOwner :exec
-- A row write, not an advisory lock: it serializes admissions and makes a
-- repeatable-read admission whose snapshot predates another's commit fail
-- with 40001 instead of counting a stale snapshot (ADR-570).
INSERT INTO org_app_quota_fences (org_id) VALUES (sqlc.arg(org_id)::uuid)
ON CONFLICT (org_id) DO UPDATE SET generation = org_app_quota_fences.generation + 1;

-- name: LockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: AccountStatus :one
SELECT status FROM accounts WHERE id = $1;

-- name: MarkDeletion :execrows
-- A repeat request never withdraws an inclusion already recorded for this
-- pending deletion (ADR-20261024150500).
UPDATE accounts SET status = 'deleted_pending',
       deletion_requested_at = coalesce(deletion_requested_at, now()),
       deletion_includes_managed_postgres = deletion_includes_managed_postgres OR sqlc.arg(include_managed_postgres)::boolean
WHERE id = sqlc.arg(id) AND status IN ('active', 'deleted_pending');

-- name: CountApps :one
SELECT count(*) FROM apps
WHERE (org_id = sqlc.narg(org_id)::uuid OR
       (org_id IS NULL AND account_id = sqlc.arg(account_id)::uuid AND sqlc.arg(personal)::boolean))
  AND status IN ('active', 'evicted_cold')
  AND CASE sqlc.arg(kind)::text
      WHEN 'developer_apps' THEN preview_of_slug IS NOT NULL AND coalesce(preview_pr_number, 0) = 0
      WHEN 'preview_apps' THEN preview_of_slug IS NOT NULL AND preview_pr_number > 0
      ELSE preview_of_slug IS NULL END;

-- name: CountOpenPreviewEnvironments :one
-- PreviewApps caps open PR preview environments: the open previews of one
-- project and pull request. A legacy preview without a project is its own
-- environment. A closed preview in its grace window holds no allowance; a
-- preview without a recorded state holds one.
SELECT count(DISTINCT coalesce(project_id::text, id::text) || '/' || preview_pr_number::text) FROM apps
WHERE (org_id = sqlc.narg(org_id)::uuid OR
       (org_id IS NULL AND account_id = sqlc.arg(account_id)::uuid AND sqlc.arg(personal)::boolean))
  AND status IN ('active', 'evicted_cold')
  AND preview_of_slug IS NOT NULL AND preview_pr_number > 0
  AND coalesce(preview_pr_state, 'open') = 'open';

-- name: CountOpenPreviewEnvironmentMembers :one
SELECT count(*) FROM apps
WHERE account_id = sqlc.arg(account_id)::uuid
  AND project_id = sqlc.arg(project_id)::uuid
  AND preview_pr_number = sqlc.arg(pr_number)::integer
  AND preview_of_slug IS NOT NULL
  AND status IN ('active', 'evicted_cold')
  AND coalesce(preview_pr_state, 'open') = 'open'
  AND id IS DISTINCT FROM sqlc.narg(app_id)::uuid;

-- name: HasLegacySharedApps :one
SELECT EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id = a.org_id
               WHERE a.account_id = $1 AND NOT o.personal_org);

-- name: CountPersonalApps :one
SELECT count(*) FROM apps
WHERE account_id = sqlc.arg(account_id)::uuid
  AND (org_id IS NULL OR org_id = (SELECT id FROM orgs
       WHERE personal_org AND personal_owner_account_id = sqlc.arg(account_id)::uuid))
  AND status IN ('active', 'evicted_cold')
  AND CASE sqlc.arg(kind)::text
      WHEN 'developer_apps' THEN preview_of_slug IS NOT NULL AND coalesce(preview_pr_number, 0) = 0
      ELSE preview_of_slug IS NULL END;
