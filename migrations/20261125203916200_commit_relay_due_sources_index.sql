-- filename: 20261125203916200_commit_relay_due_sources_index.sql
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('commit_sources_relay_due') AND NOT indisvalid) THEN
    DROP INDEX commit_sources_relay_due;
  END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS commit_sources_relay_due
    ON commit_sources (relay_next_poll_at, id) WHERE enabled AND sealed_connection IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS commit_sources_relay_due;
