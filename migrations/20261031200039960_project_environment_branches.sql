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
    -- Managed data the branch copies (PostgreSQL databases as of
    -- branch_point). Its workloads activate only once data_state is ready;
    -- apid's branch data reconciler owns the transition under a lease.
    data_state            text        NOT NULL DEFAULT 'ready',
    data_error            text        NOT NULL DEFAULT '',
    data_attempts         integer     NOT NULL DEFAULT 0,
    data_next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    data_lease_token      uuid,
    data_lease_until      timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_branches_not_self CHECK (parent_environment_id <> environment_id),
    CONSTRAINT project_environment_branches_egress_mode CHECK (egress_mode IN ('live', 'contained', 'simulated')),
    CONSTRAINT project_environment_branches_depth CHECK (depth BETWEEN 1 AND 8),
    CONSTRAINT project_environment_branches_expiry CHECK (expires_at > created_at),
    CONSTRAINT project_environment_branches_data_state CHECK (data_state IN ('pending', 'ready', 'failed')),
    CONSTRAINT project_environment_branches_data_attempts CHECK (data_attempts >= 0)
);
CREATE INDEX IF NOT EXISTS project_environment_branches_data_due_idx
    ON project_environment_branches (data_next_attempt_at) WHERE data_state = 'pending';
CREATE INDEX IF NOT EXISTS project_environment_branches_parent_idx
    ON project_environment_branches (parent_environment_id);
CREATE INDEX IF NOT EXISTS project_environment_branches_expiry_idx
    ON project_environment_branches (expires_at);
CREATE INDEX IF NOT EXISTS project_environment_branches_project_idx
    ON project_environment_branches (account_id, project_id);
COMMENT ON TABLE project_environment_branches IS
    'ADR-20261031193635 environment branches: a lazy copy of a parent project environment as of branch_point, deleted at expires_at.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS project_environment_branches;
-- +goose StatementEnd
