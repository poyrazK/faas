-- filename: 20261027141010563_agent_run_execution.sql
-- migration-compat: expand

-- +goose Up
-- The new columns and the foreign keys lock agent_runs and accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §4-§6, ADR-20261027090014 §1: what agentd records when it runs a run.
-- Every column is nullable or defaulted, so apid and older agentd keep
-- working while this lands.
ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS lease_owner text CHECK (lease_owner IS NULL OR char_length(lease_owner) BETWEEN 1 AND 128),
    ADD COLUMN IF NOT EXISTS node_id     text CHECK (node_id IS NULL OR char_length(node_id) BETWEEN 1 AND 128),
    ADD COLUMN IF NOT EXISTS instance_id text CHECK (instance_id IS NULL OR char_length(instance_id) BETWEEN 1 AND 128),
    ADD COLUMN IF NOT EXISTS exit_code   integer,
    ADD COLUMN IF NOT EXISTS signal      integer CHECK (signal IS NULL OR signal BETWEEN 0 AND 128),
    ADD COLUMN IF NOT EXISTS boot_ms     integer CHECK (boot_ms IS NULL OR boot_ms >= 0);
CREATE INDEX IF NOT EXISTS agent_runs_lease_idx ON agent_runs (lease_expires_at) WHERE lease_token IS NOT NULL;
COMMENT ON COLUMN agent_runs.lease_owner IS 'ADR-20261027090014 agentd replica holding the lease; diagnostics only.';

-- A finished run's outputs. Only a run that succeeded has a result or
-- files (ADR-20261027090010 §8). File contents go to object storage in a later step;
-- the manifest (name, size, sha256) is kept here.
CREATE TABLE IF NOT EXISTS agent_run_outputs (
    run_id        uuid PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    result        jsonb CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
    files         jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(files) = 'array'),
    log_tail      text NOT NULL DEFAULT '' CHECK (octet_length(log_tail) <= 65536),
    log_truncated boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_run_outputs_account_idx ON agent_run_outputs (account_id, created_at DESC);
COMMENT ON TABLE agent_run_outputs IS
    'ADR-20261027090010 agent run outputs written by agentd: inline result.json, the output file manifest and the log tail.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_run_outputs;
DROP INDEX IF EXISTS agent_runs_lease_idx;
ALTER TABLE agent_runs
    DROP COLUMN IF EXISTS boot_ms,
    DROP COLUMN IF EXISTS signal,
    DROP COLUMN IF EXISTS exit_code,
    DROP COLUMN IF EXISTS instance_id,
    DROP COLUMN IF EXISTS node_id,
    DROP COLUMN IF EXISTS lease_owner;
-- +goose StatementEnd
