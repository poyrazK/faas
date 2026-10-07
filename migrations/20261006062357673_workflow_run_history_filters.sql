-- +goose Up
CREATE INDEX IF NOT EXISTS workflow_runs_app_name_created_idx
    ON workflow_runs (app_id, workflow_name, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS workflow_runs_app_name_created_idx;
