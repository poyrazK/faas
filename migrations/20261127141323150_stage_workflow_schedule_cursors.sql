-- filename: 20261127141323150_stage_workflow_schedule_cursors.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000: a stage's schedule cursors are its own. Production's
-- cursors are pruned by production's definitions (on N-1 too), and a stage's
-- workflow names become per-environment in the contract release. A cursor's
-- deployment and last run are references only; admission rechecks both.
CREATE TABLE IF NOT EXISTS stage_workflow_schedule_cursors (
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    workflow_name text NOT NULL CHECK (length(workflow_name) BETWEEN 1 AND 128),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid,
    trigger_snapshot jsonb NOT NULL CHECK (jsonb_typeof(trigger_snapshot) = 'object'),
    last_evaluated_at timestamptz NOT NULL,
    scheduled_for timestamptz,
    status text NOT NULL CHECK (status IN ('armed', 'started', 'skipped_overlap', 'skipped_quota')),
    last_run_id uuid,
    last_admitted_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, workflow_name)
);
CREATE INDEX IF NOT EXISTS stage_workflow_schedule_cursors_app_idx ON stage_workflow_schedule_cursors (app_id, workflow_name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS stage_workflow_schedule_cursors;
-- +goose StatementEnd
