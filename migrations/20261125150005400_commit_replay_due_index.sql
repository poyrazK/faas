-- filename: 20261125150005400_commit_replay_due_index.sql
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('commit_replay_requests_due') AND NOT indisvalid) THEN
    DROP INDEX commit_replay_requests_due;
  END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS commit_replay_requests_due
    ON commit_replay_requests (source_id, next_attempt_at, requested_at, event_id) WHERE state = 'pending';

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS commit_replay_requests_due;
