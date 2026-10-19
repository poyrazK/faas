-- migration-compat: expand
-- filename: 20261019104819437_invocation_default_retention_indexes.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- ADR 20261019104819: schedd stamps the plan-default retention on finished
-- rows whose producer set none. Rows leave this index once stamped, so it
-- stays as small as the unstamped backlog.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_retention_unstamped_idx ON invocations (created_at)
    WHERE result_retention_until IS NULL
      AND state IN ('completed', 'failed', 'dead_letter', 'cancelled', 'superseded', 'expired');
-- +goose StatementEnd

-- +goose StatementBegin
-- The reaper orders due terminal rows by their horizon across all accounts;
-- invocations_acct_retention_idx leads with account_id and cannot serve it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_retention_due_idx ON invocations (result_retention_until)
    WHERE result_retention_until IS NOT NULL
      AND state IN ('completed', 'failed', 'dead_letter', 'cancelled', 'superseded', 'expired');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_retention_due_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_retention_unstamped_idx;
-- +goose StatementEnd
