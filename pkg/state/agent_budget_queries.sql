-- name: GetAgentKeyBudget :one
-- ADR-20261111114059: the key's own budget; no row means the plan default.
SELECT monthly_cents FROM agent_key_budgets WHERE key_id = sqlc.arg(key_id)::uuid;

-- name: UpsertAgentKeyBudget :exec
INSERT INTO agent_key_budgets (key_id, monthly_cents, updated_at, updated_by)
VALUES (sqlc.arg(key_id)::uuid, sqlc.narg(monthly_cents)::bigint, now(), sqlc.narg(updated_by)::uuid)
ON CONFLICT (key_id) DO UPDATE
SET monthly_cents = EXCLUDED.monthly_cents,
    updated_at = now(),
    updated_by = EXCLUDED.updated_by;

-- name: DeleteAgentKeyBudget :exec
DELETE FROM agent_key_budgets WHERE key_id = sqlc.arg(key_id)::uuid;

-- name: GetAgentBudgetSpend :one
SELECT spent_millicents FROM agent_budget_spend
WHERE key_id = sqlc.arg(key_id)::uuid AND month = sqlc.arg(month)::date;

-- name: AddAgentBudgetSpend :exec
-- Spend only grows: a change that lowers cost records nothing.
INSERT INTO agent_budget_spend (key_id, month, spent_millicents, updated_at)
VALUES (sqlc.arg(key_id)::uuid, sqlc.arg(month)::date, sqlc.arg(millicents)::bigint, now())
ON CONFLICT (key_id, month) DO UPDATE
SET spent_millicents = agent_budget_spend.spent_millicents + EXCLUDED.spent_millicents,
    updated_at = now();
