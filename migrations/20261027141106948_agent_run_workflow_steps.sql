-- filename: 20261027141106948_agent_run_workflow_steps.sql
-- migration-compat: expand

-- +goose Up
-- The new columns and CHECK lock agent_runs briefly; the table is still
-- dark-launched and small.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §9: a run an automation's agent step admitted names its workflow
-- run, step and the reserved workflow event that delivers its end. schedd's
-- orchestrator admits the run and delivers the event; agentd owns the run in
-- between.
ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS workflow_run_id uuid,
    ADD COLUMN IF NOT EXISTS workflow_step text
        CHECK (workflow_step IS NULL OR char_length(workflow_step) BETWEEN 1 AND 256),
    ADD COLUMN IF NOT EXISTS workflow_event text
        CHECK (workflow_event IS NULL OR workflow_event ~ '^workflow\.agent\.[0-9a-f-]{36}$');
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_runs'::regclass
        AND conname = 'agent_runs_workflow_complete_chk') THEN
        ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_workflow_complete_chk CHECK (
            (workflow_run_id IS NULL) = (workflow_step IS NULL) AND (workflow_run_id IS NULL) = (workflow_event IS NULL)
            AND (workflow_run_id IS NULL OR trigger = 'workflow')) NOT VALID;
    END IF;
END;
$do$;
-- +goose StatementEnd
ALTER TABLE agent_runs VALIDATE CONSTRAINT agent_runs_workflow_complete_chk;

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_workflow_complete_chk,
    DROP COLUMN IF EXISTS workflow_event,
    DROP COLUMN IF EXISTS workflow_step,
    DROP COLUMN IF EXISTS workflow_run_id;
-- +goose StatementEnd
