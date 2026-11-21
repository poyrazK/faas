-- filename: 20261119125548984_agent_run_capability_ledger.sql
-- migration-compat: expand

-- +goose Up
-- New tables only; the foreign keys take a brief lock on agent_runs and
-- accounts.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090016 amendment 1: outboundd's per-run capability ledger. A
-- run's summary counts its tool calls, and the conditional update on
-- tool_calls is the per-run budget until counters move in memory (step 5).
-- outboundd is the only writer of both tables.
CREATE TABLE IF NOT EXISTS agent_run_capability_summaries (
    run_id         uuid PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tool_calls     integer NOT NULL DEFAULT 0 CHECK (tool_calls >= 0),
    refused_calls  integer NOT NULL DEFAULT 0 CHECK (refused_calls >= 0),
    first_call_at  timestamptz NOT NULL DEFAULT now(),
    last_call_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE agent_run_capability_summaries IS
    'ADR-20261027090016 amendment 1: per-run capability counts written by outboundd. tool_calls are the calls admitted against the run''s tool-call budget.';

-- The low-volume decisions a seller or operator must see: a call refused by
-- the policy, by an exhausted budget, or because the tool is off.
CREATE TABLE IF NOT EXISTS agent_run_capability_decisions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id      uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tool        text NOT NULL CHECK (char_length(tool) BETWEEN 1 AND 64),
    decision    text NOT NULL CHECK (decision IN ('denied', 'budget', 'disabled')),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_run_capability_decisions_run_idx
    ON agent_run_capability_decisions (run_id, created_at);
COMMENT ON TABLE agent_run_capability_decisions IS
    'ADR-20261027090016 amendment 1: capability calls outboundd refused, by policy (denied), exhausted budget (budget) or a tool switched off (disabled).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_run_capability_decisions;
DROP TABLE IF EXISTS agent_run_capability_summaries;
-- +goose StatementEnd
