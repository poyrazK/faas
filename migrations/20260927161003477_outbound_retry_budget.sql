-- filename: 20260927161003477_outbound_retry_budget.sql

-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS retry_budget_per_minute integer NOT NULL DEFAULT 0;

-- The API applies a plan-specific ceiling. Keep a database-level gross bound
-- so operator configuration and direct SQL writes cannot create huge buckets.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_retry_budget_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_retry_budget_chk
                CHECK (retry_budget_per_minute BETWEEN 0 AND 3000);
    END IF;
END$$;
-- +goose StatementEnd

ALTER TABLE outbound_admission_state
    ADD COLUMN IF NOT EXISTS retry_budget_tokens double precision NOT NULL DEFAULT 0 CHECK (retry_budget_tokens BETWEEN 0 AND 3000),
    ADD COLUMN IF NOT EXISTS retry_budget_refilled_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS retry_budget_policy_per_minute integer NOT NULL DEFAULT 0 CHECK (retry_budget_policy_per_minute BETWEEN 0 AND 3000);

-- +goose Down
ALTER TABLE outbound_admission_state
    DROP COLUMN IF EXISTS retry_budget_policy_per_minute,
    DROP COLUMN IF EXISTS retry_budget_refilled_at,
    DROP COLUMN IF EXISTS retry_budget_tokens;

ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_retry_budget_chk,
    DROP COLUMN IF EXISTS retry_budget_per_minute;
