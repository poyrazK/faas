-- filename: 20261027141038256_agent_deliveries.sql
-- migration-compat: expand

-- +goose Up
-- The new columns and the foreign key lock agent_versions and agent_runs
-- briefly; both tables are still dark-launched and small.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §8: a version declares its deliveries, a run names where they go,
-- and agentd's delivery worker (ADR-20261027090014) records each attempt. A run that
-- succeeded and declares deliveries waits in delivering until every delivery
-- has ended.
ALTER TABLE agent_versions
    ADD COLUMN IF NOT EXISTS deliveries jsonb NOT NULL DEFAULT '[]'
        CHECK (jsonb_typeof(deliveries) = 'array' AND jsonb_array_length(deliveries) <= 4);

-- The issue or pull request a run's GitHub comments go to, and the account's
-- installation that writes them, fixed at admission.
ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS github_repository text
        CHECK (github_repository IS NULL OR github_repository ~ '^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$'),
    ADD COLUMN IF NOT EXISTS github_number bigint
        CHECK (github_number IS NULL OR github_number > 0),
    ADD COLUMN IF NOT EXISTS github_installation_id bigint
        CHECK (github_installation_id IS NULL OR github_installation_id > 0),
    ADD CONSTRAINT agent_runs_github_complete_chk CHECK (
        (github_repository IS NULL) = (github_number IS NULL)
        AND (github_repository IS NULL) = (github_installation_id IS NULL));

-- pending → sending → delivered, or back to pending (a retry) or reconciling
-- (the write's answer was lost; the next attempt reads the destination
-- first). The other ends are failed, outcome_unknown (still unresolved when
-- the window closed), superseded (a newer delivery with the same key took
-- over) and cancelled (the run was cancelled first).
CREATE TABLE IF NOT EXISTS agent_deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid NOT NULL,
    run_id           uuid NOT NULL REFERENCES agent_runs (id) ON DELETE CASCADE,
    ordinal          smallint NOT NULL CHECK (ordinal BETWEEN 0 AND 3),
    destination      text NOT NULL CHECK (destination IN ('github_comment')),
    idempotency_key  text NOT NULL CHECK (idempotency_key ~ '^[a-z0-9][a-z0-9-]{0,99}$'),
    installation_id  bigint CHECK (installation_id IS NULL OR installation_id > 0),
    repository       text CHECK (repository IS NULL OR repository ~ '^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$'),
    number           bigint CHECK (number IS NULL OR number > 0),
    body             text NOT NULL DEFAULT '' CHECK (octet_length(body) <= 65536),
    body_truncated   boolean NOT NULL DEFAULT false,
    state            text NOT NULL DEFAULT 'pending' CHECK (state IN (
                         'pending', 'sending', 'reconciling',
                         'delivered', 'failed', 'outcome_unknown', 'superseded', 'cancelled')),
    attempts         integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    deadline_at      timestamptz NOT NULL,
    lease_token      text CHECK (lease_token IS NULL OR char_length(lease_token) BETWEEN 1 AND 128),
    lease_expires_at timestamptz,
    outcome_code     text CHECK (outcome_code IS NULL OR outcome_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    outcome_message  text CHECK (outcome_message IS NULL OR char_length(outcome_message) <= 2048),
    external_id      text CHECK (external_id IS NULL OR char_length(external_id) <= 64),
    external_url     text CHECK (external_url IS NULL OR char_length(external_url) <= 512),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    CONSTRAINT agent_deliveries_run_ordinal_key UNIQUE (run_id, ordinal),
    CONSTRAINT agent_deliveries_lease_check CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)
        AND (lease_token IS NULL) = (state <> 'sending')),
    CONSTRAINT agent_deliveries_finished_check CHECK (
        (state IN ('delivered', 'failed', 'outcome_unknown', 'superseded', 'cancelled')) = (finished_at IS NOT NULL)),
    CONSTRAINT agent_deliveries_github_check CHECK (destination <> 'github_comment'
        OR (installation_id IS NOT NULL AND repository IS NOT NULL AND number IS NOT NULL))
);
-- One send per destination object at a time: two runs never write the same
-- comment at once.
CREATE UNIQUE INDEX IF NOT EXISTS agent_deliveries_sending_key_idx ON agent_deliveries (idempotency_key)
    WHERE state = 'sending';
-- A newer delivery with the same key supersedes an older one still waiting.
CREATE INDEX IF NOT EXISTS agent_deliveries_key_idx ON agent_deliveries (idempotency_key, created_at, id);
CREATE INDEX IF NOT EXISTS agent_deliveries_due_idx ON agent_deliveries (next_attempt_at)
    WHERE state IN ('pending', 'reconciling');
CREATE INDEX IF NOT EXISTS agent_deliveries_run_idx ON agent_deliveries (run_id);
COMMENT ON TABLE agent_deliveries IS
    'ADR-20261027090010 §8 deliveries of succeeded agent runs; agentd''s delivery worker (ADR-20261027090014) is the only writer.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_deliveries;
ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_github_complete_chk,
    DROP COLUMN IF EXISTS github_installation_id,
    DROP COLUMN IF EXISTS github_number,
    DROP COLUMN IF EXISTS github_repository;
ALTER TABLE agent_versions DROP COLUMN IF EXISTS deliveries;
-- +goose StatementEnd
