-- filename: 20261119171603970_agent_run_approvals.sql
-- migration-compat: expand

-- +goose Up
-- New tables only; the foreign keys take a brief lock on agent_runs and
-- accounts.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090016 amendment 2: a run's call to a tool in approve mode
-- waits for the seller's decision. outboundd writes the request; apid writes
-- the decision, because the decision is customer intent.
CREATE TABLE IF NOT EXISTS agent_run_approvals (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id             uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id         uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tool               text NOT NULL CHECK (char_length(tool) BETWEEN 1 AND 64),
    arguments_sha256   text NOT NULL CHECK (arguments_sha256 ~ '^[0-9a-f]{64}$'),
    arguments_summary  text NOT NULL DEFAULT '' CHECK (octet_length(arguments_summary) <= 2048),
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS agent_run_approvals_run_idx ON agent_run_approvals (run_id, created_at);
COMMENT ON TABLE agent_run_approvals IS
    'ADR-20261027090016 amendment 2: approval requests of agent runs, written by outboundd. arguments_summary is untrusted agent text.';

CREATE TABLE IF NOT EXISTS agent_run_approval_decisions (
    approval_id  uuid PRIMARY KEY REFERENCES agent_run_approvals(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    decision     text NOT NULL CHECK (decision IN ('approve', 'deny')),
    note         text NOT NULL DEFAULT '' CHECK (octet_length(note) <= 1024),
    decided_by   text NOT NULL DEFAULT '' CHECK (char_length(decided_by) <= 200),
    decided_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE agent_run_approval_decisions IS
    'ADR-20261027090016 amendment 2: the seller''s decision on an agent run approval, written once by apid; outboundd reads it.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_run_approval_decisions;
DROP TABLE IF EXISTS agent_run_approvals;
-- +goose StatementEnd
