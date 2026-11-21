-- filename: 20261115051534380_commit_changes_sources_app_index.sql
-- migration-compat: expand
-- ADR-20261112140000 §8: one changes source per app. Built concurrently so
-- Commit source writes never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'commit_sources_changes_app' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX commit_sources_changes_app';
 END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS commit_sources_changes_app
    ON commit_sources (app_id) WHERE queue_name IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS commit_sources_changes_app;
