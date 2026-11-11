-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- ADR-20261111114059: an agent key's monthly budget for committed cost. No
-- row means the plan's default; monthly_cents NULL means unlimited (still
-- bounded by the organization's ceiling). apid is the only writer.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_key_budgets (
    key_id        uuid PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    monthly_cents bigint CHECK (monthly_cents IS NULL OR monthly_cents BETWEEN 0 AND 100000000),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    uuid
);
-- +goose StatementEnd

-- ADR-20261111114059: committed cost an agent key added in a UTC month.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_budget_spend (
    key_id           uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    month            date NOT NULL CHECK (month = date_trunc('month', month)::date),
    spent_millicents bigint NOT NULL DEFAULT 0 CHECK (spent_millicents >= 0),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (key_id, month)
);
-- +goose StatementEnd

-- ADR-20261111114059: the organization's ceiling on every agent's budget;
-- NULL is no ceiling. The previous release's upsert leaves it untouched.
ALTER TABLE org_agent_policies ADD COLUMN IF NOT EXISTS max_agent_budget_cents bigint;

-- A budget approval records the change's added monthly cost.
ALTER TABLE agent_approval_requests ADD COLUMN IF NOT EXISTS budget_cost_millicents bigint;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'org_agent_policies_max_agent_budget_cents_check'
                     AND conrelid = 'org_agent_policies'::regclass) THEN
        ALTER TABLE org_agent_policies
            ADD CONSTRAINT org_agent_policies_max_agent_budget_cents_check
            CHECK (max_agent_budget_cents IS NULL OR max_agent_budget_cents BETWEEN 0 AND 100000000) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_approval_requests_budget_cost_check'
                     AND conrelid = 'agent_approval_requests'::regclass) THEN
        ALTER TABLE agent_approval_requests
            ADD CONSTRAINT agent_approval_requests_budget_cost_check
            CHECK (budget_cost_millicents IS NULL OR budget_cost_millicents >= 0) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE agent_approval_requests DROP CONSTRAINT IF EXISTS agent_approval_requests_budget_cost_check;
ALTER TABLE agent_approval_requests DROP COLUMN IF EXISTS budget_cost_millicents;
ALTER TABLE org_agent_policies DROP CONSTRAINT IF EXISTS org_agent_policies_max_agent_budget_cents_check;
ALTER TABLE org_agent_policies DROP COLUMN IF EXISTS max_agent_budget_cents;
DROP TABLE IF EXISTS agent_budget_spend;
DROP TABLE IF EXISTS agent_key_budgets;
