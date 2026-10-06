-- +goose Up
CREATE INDEX IF NOT EXISTS workflow_runs_app_name_concurrency_idx
    ON workflow_runs (app_id, workflow_name, status, started_at)
    WHERE status IN ('pending', 'running', 'awaiting_event');

-- +goose Down
DROP INDEX IF EXISTS workflow_runs_app_name_concurrency_idx;
