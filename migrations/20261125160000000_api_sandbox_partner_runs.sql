-- filename: 20261125160000000_api_sandbox_partner_runs.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_sandbox_runs
    ADD COLUMN partner_step_count integer NOT NULL DEFAULT 0 CHECK (partner_step_count BETWEEN 0 AND 7),
    ADD COLUMN partner_claim uuid,
    ADD COLUMN partner_claim_until timestamptz,
    ADD CONSTRAINT api_sandbox_runs_partner_claim_pair CHECK ((partner_claim IS NULL) = (partner_claim_until IS NULL));

-- +goose Down
ALTER TABLE api_sandbox_runs
    DROP CONSTRAINT api_sandbox_runs_partner_claim_pair,
    DROP COLUMN partner_claim_until,
    DROP COLUMN partner_claim,
    DROP COLUMN partner_step_count;
