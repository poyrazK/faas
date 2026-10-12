-- Store one-use operator grants for MCP task handler compatibility gates.
-- The grant is created with the deployment row and consumed in the same
-- transaction that promotes that deployment.
-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_mcp_task_migration_overrides (
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    used_at timestamptz,
    PRIMARY KEY (deployment_id)
);

-- +goose Down
DROP TABLE IF EXISTS deployment_mcp_task_migration_overrides;
