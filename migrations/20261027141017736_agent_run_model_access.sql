-- filename: 20261027141017736_agent_run_model_access.sql
-- migration-compat: expand

-- +goose Up
-- The new columns and the foreign keys lock agent_versions, agent_runs and
-- accounts briefly; all three agent tables are still dark-launched and small.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §7: a version names the account's AI gateway integration each
-- model provider reaches through the relay, and may cap one run's model
-- spend. apid writes both; NULL takes the plan's default cap.
ALTER TABLE agent_versions
    ADD COLUMN IF NOT EXISTS model_integrations jsonb NOT NULL DEFAULT '{}'
        CHECK (jsonb_typeof(model_integrations) = 'object'),
    ADD COLUMN IF NOT EXISTS ai_budget_millicents bigint
        CHECK (ai_budget_millicents IS NULL OR ai_budget_millicents BETWEEN 1 AND 1000000000000);

-- agentd records the run's effective cap when the run becomes running;
-- outboundd enforces it.
ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS ai_budget_millicents bigint
        CHECK (ai_budget_millicents IS NULL OR ai_budget_millicents BETWEEN 1 AND 1000000000000);

-- outboundd's per-run spend ledger. Settled spend lives on the run's row;
-- each admitted call holds a reservation keyed by its admission lease until
-- it settles, is released, or lapses at expires_at.
CREATE TABLE IF NOT EXISTS agent_run_ai_spend (
    run_id           uuid PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    spent_millicents bigint NOT NULL DEFAULT 0 CHECK (spent_millicents >= 0),
    calls            bigint NOT NULL DEFAULT 0 CHECK (calls >= 0),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS agent_run_ai_reservations (
    lease_id            text PRIMARY KEY CHECK (char_length(lease_id) BETWEEN 1 AND 128),
    run_id              uuid NOT NULL REFERENCES agent_run_ai_spend(run_id) ON DELETE CASCADE,
    reserved_millicents bigint NOT NULL CHECK (reserved_millicents >= 0),
    expires_at          timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_run_ai_reservations_run_idx ON agent_run_ai_reservations (run_id, expires_at);
COMMENT ON TABLE agent_run_ai_spend IS
    'ADR-20261027090010 §7 per-run model spend written by outboundd; the cap is agent_runs.ai_budget_millicents.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_run_ai_reservations;
DROP TABLE IF EXISTS agent_run_ai_spend;
ALTER TABLE agent_runs DROP COLUMN IF EXISTS ai_budget_millicents;
ALTER TABLE agent_versions
    DROP COLUMN IF EXISTS ai_budget_millicents,
    DROP COLUMN IF EXISTS model_integrations;
-- +goose StatementEnd
