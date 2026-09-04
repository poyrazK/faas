-- filename: 20260904193635713_workflows.sql
-- +goose Up
-- +goose StatementBegin

-- ADR-081 durable execution workflows — three tables:
--   public.workflow_runs   — workflow executions with definition snapshot and scheduled dispatch
--   public.workflow_steps  — step attempts within a run with status and attempt counts
--   public.workflow_events — external events injected to resume awaiting_event steps

CREATE TABLE IF NOT EXISTS workflow_runs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id              uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    workflow_name       text NOT NULL,
    status              text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','running','awaiting_event',
                                         'succeeded','failed','dead')),
    current_step        text,
    input               jsonb NOT NULL DEFAULT '{}',
    output              jsonb,
    definition_snapshot jsonb NOT NULL,
    scheduled_for       timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz,
    last_error          text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS workflow_runs_dispatch_idx
    ON workflow_runs (scheduled_for)
    WHERE status IN ('pending','running','awaiting_event');

CREATE INDEX IF NOT EXISTS workflow_runs_app_id_idx
    ON workflow_runs (app_id, created_at DESC);

CREATE TABLE IF NOT EXISTS workflow_steps (
    run_id       uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    step_name    text NOT NULL,
    status       text NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','running','awaiting_event',
                                  'succeeded','failed','dead','skipped')),
    attempt      int NOT NULL DEFAULT 0,
    input        jsonb,
    output       jsonb,
    started_at   timestamptz,
    finished_at  timestamptz,
    error        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, step_name)
);

CREATE TABLE IF NOT EXISTS workflow_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id       uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    event_name   text NOT NULL,
    payload      jsonb NOT NULL DEFAULT '{}',
    received_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS workflow_events_run_event_idx
    ON workflow_events (run_id, event_name);

CREATE OR REPLACE FUNCTION workflow_due_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE payload jsonb;
BEGIN
    payload := jsonb_build_object(
        'run_id', new.id::text,
        'app_id', new.app_id::text,
        'workflow_name', new.workflow_name,
        'status', new.status
    );
    PERFORM pg_notify('workflow_due', payload::text);
    RETURN new;
END;
$$;

DROP TRIGGER IF EXISTS workflow_due_trg ON workflow_runs;
CREATE TRIGGER workflow_due_trg
    AFTER INSERT OR UPDATE OF status ON workflow_runs
    FOR EACH ROW
    WHEN (new.status IN ('pending','running'))
    EXECUTE FUNCTION workflow_due_notify();

CREATE OR REPLACE FUNCTION workflow_event_due_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE payload jsonb;
BEGIN
    payload := jsonb_build_object(
        'run_id', new.run_id::text,
        'event_name', new.event_name
    );
    PERFORM pg_notify('workflow_due', payload::text);
    RETURN new;
END;
$$;

DROP TRIGGER IF EXISTS workflow_event_due_trg ON workflow_events;
CREATE TRIGGER workflow_event_due_trg
    AFTER INSERT ON workflow_events
    FOR EACH ROW
    EXECUTE FUNCTION workflow_event_due_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS workflow_due_trg ON workflow_runs;
DROP FUNCTION IF EXISTS workflow_due_notify();
DROP TRIGGER IF EXISTS workflow_event_due_trg ON workflow_events;
DROP FUNCTION IF EXISTS workflow_event_due_notify();
DROP TABLE IF EXISTS workflow_events CASCADE;
DROP TABLE IF EXISTS workflow_steps CASCADE;
DROP TABLE IF EXISTS workflow_runs CASCADE;
-- +goose StatementEnd
