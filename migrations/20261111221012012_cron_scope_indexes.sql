-- filename: 20261111221012012_cron_scope_indexes.sql
-- migration-compat: expand
-- ADR-20261104120000: a stage may hold the same schedule, path and command
-- as production, so a cron's identity is unique within one environment of
-- an app. Environment deletion finds its crons by environment_id. Both
-- indexes are built concurrently so cron writes never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'crons_environment_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX crons_environment_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'crons_app_scope_schedule_path_command_unique' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX crons_app_scope_schedule_path_command_unique';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS crons_environment_idx
    ON crons (environment_id) WHERE environment_id IS NOT NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS crons_app_scope_schedule_path_command_unique
    ON crons (app_id, deployment_scope, schedule, path, command);

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS crons_app_scope_schedule_path_command_unique;
DROP INDEX CONCURRENTLY IF EXISTS crons_environment_idx;
