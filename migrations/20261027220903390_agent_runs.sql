-- filename: 20261027220903390_agent_runs.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock accounts and platform_tenants briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §4: agent definitions, their immutable versions, and runs. apid
-- writes agents and versions and inserts runs as queued; agentd (ADR-20261027090014)
-- owns every later run transition. Gated by the internal agents capability.
CREATE TABLE IF NOT EXISTS agents (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name           text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    description    text NOT NULL DEFAULT '' CHECK (char_length(description) <= 1024),
    state          text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'deleted')),
    latest_version integer NOT NULL DEFAULT 0 CHECK (latest_version >= 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    deleted_at     timestamptz,
    CONSTRAINT agents_deleted_at_check CHECK ((state = 'deleted') = (deleted_at IS NOT NULL)),
    CONSTRAINT agents_account_id_id_key UNIQUE (account_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS agents_account_name_active_idx ON agents (account_id, name) WHERE state = 'active';
COMMENT ON TABLE agents IS
    'ADR-20261027090010 agent definitions; apid is the only writer. Deleted agents keep their versions and runs.';

-- A version is immutable once written: the harness, the prompt template and
-- the size and time asked for.
CREATE TABLE IF NOT EXISTS agent_versions (
    agent_id        uuid NOT NULL,
    version         integer NOT NULL CHECK (version >= 1),
    account_id      uuid NOT NULL,
    harness_preset  text CHECK (harness_preset IS NULL OR harness_preset IN ('codex', 'opencode', 'claude-code')),
    harness_image   text CHECK (harness_image IS NULL OR char_length(harness_image) BETWEEN 1 AND 512),
    harness_run     text[] NOT NULL DEFAULT '{}' CHECK (cardinality(harness_run) <= 64),
    harness_models  jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(harness_models) = 'object'),
    prompt_template text NOT NULL CHECK (octet_length(prompt_template) BETWEEN 1 AND 65536),
    ram_mb          integer CHECK (ram_mb IS NULL OR (ram_mb BETWEEN 512 AND 4096 AND ram_mb % 512 = 0)),
    timeout_sec     integer NOT NULL CHECK (timeout_sec BETWEEN 1 AND 3600),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, version),
    CONSTRAINT agent_versions_harness_check CHECK ((harness_preset IS NULL) <> (harness_image IS NULL)),
    CONSTRAINT agent_versions_image_run_check CHECK (harness_image IS NULL OR cardinality(harness_run) >= 1),
    CONSTRAINT agent_versions_agent_fkey FOREIGN KEY (account_id, agent_id)
        REFERENCES agents (account_id, id) ON DELETE CASCADE
);
COMMENT ON TABLE agent_versions IS
    'ADR-20261027090010 immutable agent versions: harness descriptor, prompt template, size and timeout.';

-- queued → starting → running → delivering, then one terminal state.
-- Terminal states are immutable and carry finished_at.
CREATE TABLE IF NOT EXISTS agent_runs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL,
    agent_id            uuid NOT NULL,
    agent_version       integer NOT NULL,
    tenant_id           uuid,
    trigger             text NOT NULL CHECK (trigger IN ('api', 'automation', 'workflow', 'trigger')),
    dedupe_key          text CHECK (dedupe_key IS NULL OR char_length(dedupe_key) BETWEEN 1 AND 200),
    prompt              text CHECK (prompt IS NULL OR octet_length(prompt) <= 65536),
    inputs              jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(inputs) = 'object'),
    state               text NOT NULL DEFAULT 'queued' CHECK (state IN (
                            'queued', 'starting', 'running', 'delivering',
                            'succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')),
    ram_mb              integer CHECK (ram_mb IS NULL OR (ram_mb BETWEEN 512 AND 4096 AND ram_mb % 512 = 0)),
    timeout_sec         integer NOT NULL CHECK (timeout_sec BETWEEN 1 AND 3600),
    attempt             integer NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    lease_token         text CHECK (lease_token IS NULL OR char_length(lease_token) BETWEEN 1 AND 128),
    lease_expires_at    timestamptz,
    cancel_requested_at timestamptz,
    outcome_code        text CHECK (outcome_code IS NULL OR outcome_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    outcome_message     text CHECK (outcome_message IS NULL OR char_length(outcome_message) <= 2048),
    queued_at           timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    running_at          timestamptz,
    finished_at         timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agent_runs_lease_check CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT agent_runs_finished_check CHECK (
        (state IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')) = (finished_at IS NOT NULL)),
    CONSTRAINT agent_runs_agent_fkey FOREIGN KEY (account_id, agent_id)
        REFERENCES agents (account_id, id) ON DELETE CASCADE,
    CONSTRAINT agent_runs_version_fkey FOREIGN KEY (agent_id, agent_version)
        REFERENCES agent_versions (agent_id, version) ON DELETE CASCADE,
    CONSTRAINT agent_runs_tenant_fkey FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants (account_id, id) ON DELETE CASCADE
);
-- One run per dedupe key, per agent and tenant (a run without a tenant is
-- the seller's own).
CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_dedupe_idx ON agent_runs (agent_id, tenant_id, dedupe_key)
    NULLS NOT DISTINCT WHERE dedupe_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS agent_runs_account_queued_idx ON agent_runs (account_id, queued_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS agent_runs_queue_idx ON agent_runs (queued_at, id) WHERE state = 'queued';
CREATE INDEX IF NOT EXISTS agent_runs_live_idx ON agent_runs (account_id, state)
    WHERE state IN ('queued', 'starting', 'running', 'delivering');
COMMENT ON TABLE agent_runs IS
    'ADR-20261027090010 agent runs; apid inserts queued runs and cancels queued ones, agentd (ADR-20261027090014) owns every other transition.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_runs;
DROP TABLE IF EXISTS agent_versions;
DROP TABLE IF EXISTS agents;
-- +goose StatementEnd
