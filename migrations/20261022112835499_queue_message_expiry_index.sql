-- migration-compat: expand
-- filename: 20261022112835499_queue_message_expiry_index.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- ADR-20261022105723: schedd moves pending queue messages past their plan's
-- retention to the dead letter, oldest first. Rows leave this index when a
-- worker claims them, so it stays as small as the pending queue backlog.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_queue_pending_created_idx ON invocations (created_at)
    WHERE source = 'queue' AND state = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_queue_pending_created_idx;
-- +goose StatementEnd
