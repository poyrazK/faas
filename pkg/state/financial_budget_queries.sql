-- ADR-566. Policy mutations serialize per account and publish an atomic audit.
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

-- ADR-1500. Enabled monitored notify budgets across accounts, in a stable order.
-- name: FinancialBudgetNotifyPolicies :many
SELECT id, account_id, revision, spec, created_at, updated_at, deleted_at
FROM financial_budget_policies
WHERE deleted_at IS NULL
  AND spec->>'enabled' = 'true'
  AND spec->>'mode' = 'monitored'
  AND spec->>'action' = 'notify'
ORDER BY account_id, id LIMIT sqlc.arg(page_size)::integer;

-- A threshold notifies at most once per budget and UTC month.
-- name: FinancialBudgetNotificationClaim :execrows
INSERT INTO financial_budget_notifications(account_id, policy_id, period_start, threshold_millicents, policy_revision, known_millicents, coverage_complete)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(policy_id)::uuid, sqlc.arg(period_start)::timestamptz,
  sqlc.arg(threshold_millicents)::bigint, sqlc.arg(policy_revision)::bigint, sqlc.arg(known_millicents)::bigint,
  sqlc.arg(coverage_complete)::boolean)
ON CONFLICT (policy_id, period_start, threshold_millicents) DO NOTHING;

-- name: FinancialBudgetNotificationsPending :many
SELECT account_id, policy_id, period_start, threshold_millicents, policy_revision, known_millicents,
  coverage_complete, status, attempts, last_error, claimed_at, finished_at
FROM financial_budget_notifications
WHERE status = 'pending'
ORDER BY claimed_at, policy_id, period_start, threshold_millicents LIMIT sqlc.arg(page_size)::integer;

-- One delivery outcome covers every pending threshold of a budget and month up
-- to the one the email named. 'failed' counts the attempt and stays pending.
-- name: FinancialBudgetNotificationsRecord :execrows
UPDATE financial_budget_notifications
SET status = CASE WHEN sqlc.arg(outcome)::text = 'failed' THEN 'pending' ELSE sqlc.arg(outcome)::text END,
  attempts = attempts + CASE WHEN sqlc.arg(outcome)::text = 'cancelled' THEN 0 ELSE 1 END,
  last_error = sqlc.arg(last_error)::text,
  finished_at = CASE WHEN sqlc.arg(outcome)::text = 'failed' THEN NULL ELSE clock_timestamp() END
WHERE policy_id = sqlc.arg(policy_id)::uuid AND period_start = sqlc.arg(period_start)::timestamptz
  AND threshold_millicents <= sqlc.arg(max_threshold_millicents)::bigint AND status = 'pending';
