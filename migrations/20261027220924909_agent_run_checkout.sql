-- filename: 20261027220924909_agent_run_checkout.sql
-- migration-compat: expand

-- +goose Up
-- The new columns lock agent_runs briefly; the table is still dark-launched
-- and small.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §3: a run may ask for a repository's tree at one commit. apid
-- records the repository, the full commit SHA and the account's GitHub
-- installation that fetches it; agentd prepares the input drive from them.
ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS checkout_repository text
        CHECK (checkout_repository IS NULL OR checkout_repository ~ '^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$'),
    ADD COLUMN IF NOT EXISTS checkout_commit text
        CHECK (checkout_commit IS NULL OR checkout_commit ~ '^[0-9a-f]{40}$'),
    ADD COLUMN IF NOT EXISTS checkout_installation_id bigint
        CHECK (checkout_installation_id IS NULL OR checkout_installation_id > 0);
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_runs'::regclass
        AND conname = 'agent_runs_checkout_complete_chk') THEN
        ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_checkout_complete_chk CHECK (
            (checkout_repository IS NULL) = (checkout_commit IS NULL)
            AND (checkout_repository IS NULL) = (checkout_installation_id IS NULL));
    END IF;
END;
$do$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_checkout_complete_chk,
    DROP COLUMN IF EXISTS checkout_installation_id,
    DROP COLUMN IF EXISTS checkout_commit,
    DROP COLUMN IF EXISTS checkout_repository;
-- +goose StatementEnd
