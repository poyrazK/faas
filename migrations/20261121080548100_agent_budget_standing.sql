-- filename: 20261121080548100_agent_budget_standing.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Cost-control review 2026-11-19 (N9, N10): an agent key's budget holds the
-- always-on cost its changes keep running, not what it added this month. The
-- monthly rows reset on the 1st, so cost an agent added in one month was free
-- against the next month's budget. One row per key, reserved atomically
-- before a change is written; a change that lowers cost gives budget back.
-- agent_budget_spend stays for the previous release.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_budget_standing (
    key_id              uuid PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    standing_millicents bigint NOT NULL DEFAULT 0 CHECK (standing_millicents >= 0),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- Start each key from what it added this month, the budget it was held to.
-- +goose StatementBegin
INSERT INTO agent_budget_standing (key_id, standing_millicents)
SELECT key_id, spent_millicents FROM agent_budget_spend
 WHERE month = date_trunc('month', now() AT TIME ZONE 'UTC')::date AND spent_millicents > 0
ON CONFLICT (key_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS agent_budget_standing;
