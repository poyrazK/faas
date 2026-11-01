-- filename: 20261101215610393_project_environment_branch_databases.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261031193635 slice 2: one row per managed PostgreSQL copy an
-- environment branch owns. The row is written before the restore so a copy is
-- never untracked, and it has no foreign key to the environment: it outlives a
-- deleted or expired branch until apid's branch data reconciler has deleted the
-- copy and its bindings. A new table, so older apid keeps working.
CREATE TABLE IF NOT EXISTS project_environment_branch_databases (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id         uuid        NOT NULL,
    environment_id     uuid        NOT NULL,
    environment_slug   text        NOT NULL,
    source_database_id uuid        NOT NULL,
    copy_name          text        NOT NULL,
    copy_database_id   uuid,
    point_in_time      timestamptz NOT NULL,
    lease_token        uuid,
    lease_until        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_branch_databases_one_copy UNIQUE (environment_id, source_database_id),
    CONSTRAINT project_environment_branch_databases_copy_name UNIQUE (account_id, copy_name)
);
CREATE INDEX IF NOT EXISTS project_environment_branch_databases_environment_idx
    ON project_environment_branch_databases (environment_id);
COMMENT ON TABLE project_environment_branch_databases IS
    'ADR-20261031193635 managed PostgreSQL copies owned by an environment branch; deleted with their bindings after the branch is gone.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS project_environment_branch_databases;
-- +goose StatementEnd
