-- +goose Up
CREATE TABLE project_deploy_rollback_members (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deploy_group_id uuid NOT NULL
);

CREATE INDEX project_deploy_rollback_members_group_idx
    ON project_deploy_rollback_members (project_id, deploy_group_id);

-- +goose Down
DROP TABLE project_deploy_rollback_members;
