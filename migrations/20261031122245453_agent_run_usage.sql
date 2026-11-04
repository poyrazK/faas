-- filename: 20261031122245453_agent_run_usage.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock agent_runs and accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090011 rollout step 2: vmmd's measured memory of each run VM,
-- recorded once per run while agentd holds its lease. Nothing bills it yet;
-- it is compared with configured-RAM billing on dogfood runs. MB is MiB and
-- *_mb_ms sums MB × milliseconds. A new table, so apid and older agentd keep
-- working while this lands.
CREATE TABLE IF NOT EXISTS agent_run_usage (
    run_id                 uuid PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id             uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    method                 smallint NOT NULL CHECK (method >= 1),
    samples                integer NOT NULL CHECK (samples >= 0),
    expected_samples       integer NOT NULL CHECK (expected_samples >= samples),
    measured_ms            bigint NOT NULL CHECK (measured_ms >= 0),
    paused_ms              bigint NOT NULL CHECK (paused_ms BETWEEN 0 AND measured_ms),
    private_mb_ms          bigint NOT NULL CHECK (private_mb_ms >= 0),
    unpaused_private_mb_ms bigint NOT NULL CHECK (unpaused_private_mb_ms BETWEEN 0 AND private_mb_ms),
    billable_mb_ms         bigint NOT NULL CHECK (billable_mb_ms >= 0),
    peak_private_mb        bigint NOT NULL CHECK (peak_private_mb >= 0),
    cold_boot              boolean NOT NULL,
    recorded_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_run_usage_account_idx ON agent_run_usage (account_id, recorded_at DESC);
COMMENT ON TABLE agent_run_usage IS
    'ADR-20261027090011 measured private memory of agent run VMs, written by agentd from vmmd''s usage frame; not billed yet.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_run_usage;
-- +goose StatementEnd
