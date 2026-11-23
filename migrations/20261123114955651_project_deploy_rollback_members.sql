-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS project_deploy_rollback_members (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deploy_group_id uuid NOT NULL
);

CREATE INDEX IF NOT EXISTS project_deploy_rollback_members_group_idx
    ON project_deploy_rollback_members (project_id, deploy_group_id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE project_deploy_rollback_members;
