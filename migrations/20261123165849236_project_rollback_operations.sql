-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS project_rollback_operations (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('running', 'blocked', 'complete')),
    version bigint NOT NULL DEFAULT 0,
    next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    receipt jsonb NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS project_rollback_one_running
    ON project_rollback_operations (project_id) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS project_rollback_due
    ON project_rollback_operations (next_run_at, id) WHERE status = 'running';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE project_rollback_operations;
