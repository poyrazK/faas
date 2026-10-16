-- ADR-1285: schedd reports Operation outcomes of the last fifteen minutes
-- from their events. Outcome events are a small share of the table, so a
-- partial index on their time keeps that read off a full scan.
--
-- Not CONCURRENTLY: pkg/db.MigrateUp runs every file in a transaction. The
-- events table is small while Operations admission is in preview.
-- +goose Up
CREATE INDEX IF NOT EXISTS customer_operation_events_outcome_idx ON customer_operation_events (created_at)
    WHERE event_type IN ('succeeded', 'failed', 'cancelled', 'reconciliation_required');

-- +goose Down
DROP INDEX IF EXISTS customer_operation_events_outcome_idx;
