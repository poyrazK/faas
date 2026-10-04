-- ADR-530. Policy mutations serialize per account and publish an atomic audit.
-- name: FinancialBudgetAccountLock :one
SELECT id FROM accounts WHERE id = sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: FinancialBudgetScopeOwned :one
SELECT CASE sqlc.arg(scope_kind)::text
  WHEN 'account' THEN EXISTS(SELECT 1 FROM accounts WHERE id = sqlc.arg(account_id)::uuid)
  WHEN 'app' THEN EXISTS(SELECT 1 FROM apps WHERE id = sqlc.narg(scope_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND status != 'deleted')
  WHEN 'job' THEN EXISTS(SELECT 1 FROM jobs WHERE id = sqlc.narg(scope_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND status != 'deleted')
  WHEN 'project' THEN EXISTS(SELECT 1 FROM projects WHERE id = sqlc.narg(scope_id)::uuid AND account_id = sqlc.arg(account_id)::uuid)
  WHEN 'environment' THEN EXISTS(SELECT 1 FROM project_environments WHERE id = sqlc.narg(scope_id)::uuid AND account_id = sqlc.arg(account_id)::uuid)
  ELSE false END::boolean AS owned;

-- name: FinancialBudgetCount :one
SELECT count(*)::bigint FROM financial_budget_policies WHERE account_id = sqlc.arg(account_id)::uuid AND deleted_at IS NULL;

-- name: FinancialBudgetInsert :one
INSERT INTO financial_budget_policies(id, account_id, revision, spec)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, 1, sqlc.arg(spec)::jsonb)
RETURNING id, account_id, revision, spec, created_at, updated_at, deleted_at;

-- name: FinancialBudgetUpdate :one
UPDATE financial_budget_policies
SET spec = sqlc.arg(spec)::jsonb, revision = revision + 1, updated_at = clock_timestamp()
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid
  AND revision = sqlc.arg(expected_revision)::bigint AND deleted_at IS NULL
RETURNING id, account_id, revision, spec, created_at, updated_at, deleted_at;

-- name: FinancialBudgetDelete :one
UPDATE financial_budget_policies
SET deleted_at = clock_timestamp(), updated_at = clock_timestamp(), revision = revision + 1
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid
  AND revision = sqlc.arg(expected_revision)::bigint AND deleted_at IS NULL
RETURNING id, account_id, revision, spec, created_at, updated_at, deleted_at;

-- name: FinancialBudgetGet :one
SELECT id, account_id, revision, spec, created_at, updated_at, deleted_at
FROM financial_budget_policies WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid;

-- name: FinancialBudgetList :many
SELECT id, account_id, revision, spec, created_at, updated_at, deleted_at
FROM financial_budget_policies
WHERE account_id = sqlc.arg(account_id)::uuid AND deleted_at IS NULL
ORDER BY id LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialBudgetRevisionInsert :exec
INSERT INTO financial_budget_revisions(account_id, policy_id, revision, actor, mutation, spec)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(policy_id)::uuid, sqlc.arg(revision)::bigint, sqlc.arg(actor)::text, sqlc.arg(mutation)::text, sqlc.arg(spec)::jsonb);

-- name: FinancialBudgetRevisionList :many
SELECT account_id, policy_id, revision, actor, mutation, spec, recorded_at
FROM financial_budget_revisions
WHERE account_id = sqlc.arg(account_id)::uuid AND policy_id = sqlc.arg(policy_id)::uuid
  AND revision > sqlc.arg(after_revision)::bigint
ORDER BY revision LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialBudgetAppActionEligible :one
SELECT EXISTS(
  SELECT 1 FROM apps WHERE id = sqlc.arg(app_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
    AND status != 'deleted'
    AND CASE sqlc.arg(action)::text
      WHEN 'stop_previews' THEN preview_of_slug IS NOT NULL AND preview_of_slug != ''
      WHEN 'suspend_background' THEN workload_class IN ('worker','job')
      ELSE true END
)::boolean AS eligible;
