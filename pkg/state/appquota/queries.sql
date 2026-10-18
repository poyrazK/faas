-- name: ResolveOwner :one
SELECT id, plan, status, deleted_pending, personal_org, personal_owner_account_id
FROM orgs
WHERE id = sqlc.narg(org_id)::uuid
   OR (sqlc.narg(org_id)::uuid IS NULL AND personal_org AND personal_owner_account_id = sqlc.arg(account_id)::uuid)
FOR SHARE;

-- name: LockOwner :exec
SELECT pg_advisory_xact_lock(hashtextextended('org-app-quota:' || sqlc.arg(org_id)::uuid::text, 0));

-- name: LockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: AccountStatus :one
SELECT status FROM accounts WHERE id = $1;

-- name: MarkDeletion :execrows
UPDATE accounts SET status = 'deleted_pending',
       deletion_requested_at = coalesce(deletion_requested_at, now())
WHERE id = $1 AND status IN ('active', 'deleted_pending');

-- name: CountApps :one
SELECT count(*) FROM apps
WHERE (org_id = sqlc.narg(org_id)::uuid OR
       (org_id IS NULL AND account_id = sqlc.arg(account_id)::uuid AND sqlc.arg(personal)::boolean))
  AND status IN ('active', 'evicted_cold')
  AND CASE sqlc.arg(kind)::text
      WHEN 'developer_apps' THEN preview_of_slug IS NOT NULL AND coalesce(preview_pr_number, 0) = 0
      WHEN 'preview_apps' THEN preview_of_slug IS NOT NULL AND preview_pr_number > 0
      ELSE preview_of_slug IS NULL END;

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
