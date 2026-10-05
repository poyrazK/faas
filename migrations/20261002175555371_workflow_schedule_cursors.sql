-- filename: 20261002175555371_workflow_schedule_cursors.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS workflow_schedule_cursors (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    workflow_name text NOT NULL CHECK (workflow_name <> ''),
    deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
    trigger_snapshot jsonb NOT NULL CHECK (jsonb_typeof(trigger_snapshot) = 'object'),
    last_evaluated_at timestamptz NOT NULL,
    scheduled_for timestamptz,
    status text NOT NULL CHECK (status IN ('armed', 'started', 'skipped_overlap', 'skipped_quota')),
    last_run_id uuid REFERENCES workflow_runs(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, workflow_name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS workflow_schedule_cursors;
-- +goose StatementEnd
