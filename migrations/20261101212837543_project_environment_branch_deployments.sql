-- filename: 20261101212837543_project_environment_branch_deployments.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock their parents briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261031193635: the deployments a branch created from its parent's
-- artifacts. Only these activate without a boot; a later deploy into the branch
-- primes normally. source_deployment_id is the lineage a later restore will
-- read, kept without a foreign key because the parent's deployment may be
-- retired first. A new table, so apid without branch support keeps working.
CREATE TABLE IF NOT EXISTS project_environment_branch_deployments (
    deployment_id        uuid        PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    environment_id       uuid        NOT NULL REFERENCES project_environment_branches(environment_id) ON DELETE CASCADE,
    app_id               uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    source_deployment_id uuid        NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_branch_deployments_not_self CHECK (source_deployment_id <> deployment_id),
    CONSTRAINT project_environment_branch_deployments_one_per_app UNIQUE (environment_id, app_id)
);
COMMENT ON TABLE project_environment_branch_deployments IS
    'ADR-20261031193635 deployments an environment branch created from its parent''s artifacts; they activate on first request.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS project_environment_branch_deployments;
-- +goose StatementEnd
