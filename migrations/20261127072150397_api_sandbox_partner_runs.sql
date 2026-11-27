-- filename: 20261127072150397_api_sandbox_partner_runs.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_sandbox_runs
    ADD COLUMN IF NOT EXISTS partner_step_count integer NOT NULL DEFAULT 0 CHECK (partner_step_count BETWEEN 0 AND 7),
    ADD COLUMN IF NOT EXISTS partner_claim uuid,
    ADD COLUMN IF NOT EXISTS partner_claim_until timestamptz;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'api_sandbox_runs'::regclass AND conname = 'api_sandbox_runs_partner_claim_pair'
    ) THEN
        ALTER TABLE api_sandbox_runs ADD CONSTRAINT api_sandbox_runs_partner_claim_pair
            CHECK ((partner_claim IS NULL) = (partner_claim_until IS NULL));
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE api_sandbox_runs
    DROP CONSTRAINT api_sandbox_runs_partner_claim_pair,
    DROP COLUMN partner_claim_until,
    DROP COLUMN partner_claim,
    DROP COLUMN partner_step_count;
