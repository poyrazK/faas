-- filename: 20261031200039960_project_environment_branches.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock their parents briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261031193635: an environment branch is a project environment with a
-- parent and a branch point. One row per branch; the environment row itself is
-- an ordinary project_environments row. A new table, so apid without branch
-- support keeps working while this lands.
CREATE TABLE IF NOT EXISTS project_environment_branches (
    environment_id        uuid        PRIMARY KEY REFERENCES project_environments(id) ON DELETE CASCADE,
    account_id            uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id            uuid        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    -- NO ACTION, not RESTRICT: a parent can't be deleted while a branch of it
    -- exists, but a project or account delete removes both in one statement.
    parent_environment_id uuid        NOT NULL REFERENCES project_environments(id),
    branch_point          timestamptz NOT NULL,
    expires_at            timestamptz NOT NULL,
    egress_mode           text        NOT NULL DEFAULT 'live',
    depth                 smallint    NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_branches_not_self CHECK (parent_environment_id <> environment_id),
    CONSTRAINT project_environment_branches_egress_mode CHECK (egress_mode IN ('live', 'contained', 'simulated')),
    CONSTRAINT project_environment_branches_depth CHECK (depth BETWEEN 1 AND 8),
    CONSTRAINT project_environment_branches_expiry CHECK (expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS project_environment_branches_parent_idx
    ON project_environment_branches (parent_environment_id);
CREATE INDEX IF NOT EXISTS project_environment_branches_expiry_idx
    ON project_environment_branches (expires_at);
CREATE INDEX IF NOT EXISTS project_environment_branches_project_idx
    ON project_environment_branches (account_id, project_id);
COMMENT ON TABLE project_environment_branches IS
    'ADR-20261031193635 environment branches: a lazy copy of a parent project environment as of branch_point, deleted at expires_at.';

-- The deployments a branch created from its parent's artifacts. Only these
-- activate without a boot; a later deploy into the branch primes normally.
-- source_deployment_id is the lineage a later restore will read, kept without
-- a foreign key because the parent's deployment may be retired first.
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
DROP TABLE IF EXISTS project_environment_branches;
-- +goose StatementEnd
