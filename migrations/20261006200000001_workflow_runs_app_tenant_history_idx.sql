-- filename: 20261006200000001_workflow_runs_app_tenant_history_idx.sql

-- +goose Up
CREATE INDEX IF NOT EXISTS workflow_runs_app_tenant_history_idx
    ON workflow_runs (app_id, platform_tenant_id, created_at DESC, id DESC)
    WHERE platform_tenant_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS workflow_runs_app_tenant_history_idx;
