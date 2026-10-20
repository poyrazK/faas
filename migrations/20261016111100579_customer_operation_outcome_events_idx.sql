-- migration-compat: expand
-- ADR-20261016123221: schedd reports Operation outcomes of the last fifteen minutes
-- from their events. Outcome events are a small share of the table, so a
-- partial index on their time keeps that read off a full scan. Built
-- concurrently so customer_operation_events keeps taking writes; replacing an
-- interrupted build also removes the invalid index PostgreSQL leaves behind.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS customer_operation_events_outcome_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS customer_operation_events_outcome_idx ON customer_operation_events (created_at)
    WHERE event_type IN ('succeeded', 'failed', 'cancelled', 'reconciliation_required');
RESET lock_timeout;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS customer_operation_events_outcome_idx;
