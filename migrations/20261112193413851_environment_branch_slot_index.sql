-- filename: 20261112193413851_environment_branch_slot_index.sql
-- migration-compat: expand
-- ADR-20261031193635 stable branch URLs: the gateway resolves a slot to the
-- branch that holds it. One branch holds a slot at a time (a project's
-- environment slugs are unique), so the index is unique. It is built
-- concurrently so branch writes never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'project_environment_branches_slot_unique' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX project_environment_branches_slot_unique';
 END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS project_environment_branches_slot_unique
    ON project_environment_branches (slot_id) WHERE slot_id IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS project_environment_branches_slot_unique;
