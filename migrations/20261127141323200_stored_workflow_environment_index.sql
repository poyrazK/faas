-- filename: 20261127141323200_stored_workflow_environment_index.sql
-- migration-compat: expand
-- ADR-20261104120000 amendment 2026-11-22: lists a stage's stored workflows,
-- and makes a workflow's name unique within one environment, so a stage can
-- hold a copy of a production workflow under the same name once a later
-- contract migration moves stored_workflows_pkey off (app_id, name).
-- Production is the zero UUID in the identity. Built concurrently so
-- workflow writes never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'stored_workflows_environment_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX stored_workflows_environment_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'stored_workflows_app_environment_name_uniq' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX stored_workflows_app_environment_name_uniq';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS stored_workflows_environment_idx
    ON stored_workflows (environment_id) WHERE environment_id IS NOT NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS stored_workflows_app_environment_name_uniq
    ON stored_workflows (app_id, name, (COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'::uuid)));

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS stored_workflows_app_environment_name_uniq;
DROP INDEX CONCURRENTLY IF EXISTS stored_workflows_environment_idx;
