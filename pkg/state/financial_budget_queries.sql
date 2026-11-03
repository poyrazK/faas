-- ADR-566. Policy mutations serialize per account and publish an atomic audit.
-- name: FinancialBudgetAccountLock :one
SELECT id FROM resource_namespaces WHERE id = sqlc.arg(account_id)::uuid FOR UPDATE;

-- A budget covers what its billing subject pays for (ADR-20261031211500): an
-- organization that owns its resources pays for its apps and projects, and the
-- member who created one does not. Usage evidence is recorded under the same
-- subject, so a scope billed elsewhere would read no spending.
-- name: FinancialBudgetScopeOwned :one
SELECT CASE sqlc.arg(scope_kind)::text
  WHEN 'account' THEN EXISTS(SELECT 1 FROM resource_namespaces WHERE id = sqlc.arg(account_id)::uuid)
  WHEN 'app' THEN EXISTS(SELECT 1 FROM apps a
    LEFT JOIN orgs o ON o.id = a.org_id AND o.resource_ownership_enabled AND NOT o.personal_org
    WHERE a.id = sqlc.narg(scope_id)::uuid AND a.status != 'deleted'
      AND CASE WHEN o.id IS NULL THEN a.account_id = sqlc.arg(account_id)::uuid ELSE o.id = sqlc.arg(account_id)::uuid END)
  WHEN 'job' THEN EXISTS(SELECT 1 FROM jobs WHERE id = sqlc.narg(scope_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND status != 'deleted')
  WHEN 'project' THEN EXISTS(SELECT 1 FROM projects p
    LEFT JOIN orgs o ON o.id = p.org_id AND o.resource_ownership_enabled AND NOT o.personal_org
    WHERE p.id = sqlc.narg(scope_id)::uuid
      AND CASE WHEN o.id IS NULL THEN p.account_id = sqlc.arg(account_id)::uuid ELSE o.id = sqlc.arg(account_id)::uuid END)
  WHEN 'environment' THEN EXISTS(SELECT 1 FROM project_environments e
    JOIN projects p ON p.id = e.project_id
    LEFT JOIN orgs o ON o.id = p.org_id AND o.resource_ownership_enabled AND NOT o.personal_org
    WHERE e.id = sqlc.narg(scope_id)::uuid
      AND CASE WHEN o.id IS NULL THEN e.account_id = sqlc.arg(account_id)::uuid ELSE o.id = sqlc.arg(account_id)::uuid END)
  ELSE false END::boolean AS owned;

-- The subjects meterd evaluates: one read per pass instead of one per account.
-- name: FinancialBudgetMonitoredSubjects :many
SELECT DISTINCT account_id FROM financial_budget_policies
WHERE deleted_at IS NULL AND spec->>'enabled' = 'true' AND spec->>'mode' = 'monitored'
ORDER BY account_id;

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
  SELECT 1 FROM apps a
  LEFT JOIN orgs o ON o.id = a.org_id AND o.resource_ownership_enabled AND NOT o.personal_org
  WHERE a.id = sqlc.arg(app_id)::uuid AND a.status != 'deleted'
    AND CASE WHEN o.id IS NULL THEN a.account_id = sqlc.arg(account_id)::uuid ELSE o.id = sqlc.arg(account_id)::uuid END
    AND CASE sqlc.arg(action)::text
      WHEN 'stop_previews' THEN a.preview_of_slug IS NOT NULL AND a.preview_of_slug != ''
      WHEN 'suspend_background' THEN a.workload_class IN ('worker','job')
      ELSE true END
)::boolean AS eligible;
