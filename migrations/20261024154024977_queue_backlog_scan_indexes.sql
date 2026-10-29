-- migration-compat: expand
-- filename: 20261024154024977_queue_backlog_scan_indexes.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- ADR-20261022105723 amendment 2026-10-24: queue backlogs reach 1,000,000
-- messages per app, and the paths below must not read past them.
--
-- The invocation drain never delivers a named-queue message (a queue name or
-- binding); push pollers and pull leases do. Walking invocations_due_idx it
-- skipped every such row before reaching any app's due work: 420,000 rows,
-- 315 ms per drain tick, on the 2026-10-24 benchmark. This index holds only
-- what the drain can deliver.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_drain_due_idx ON invocations (due_at, id)
    WHERE state = 'pending' AND (source <> 'queue' OR (queue_name = '' AND queue_binding_id IS NULL));
-- +goose StatementEnd

-- +goose StatementBegin
-- A pull lease reads a binding's pending messages in due order, so it stops
-- after its candidate limit instead of filtering the global due index.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_queue_lease_binding_idx
    ON invocations (app_id, queue_binding_id, due_at, created_at, id)
    WHERE source = 'queue' AND state = 'pending' AND queue_binding_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- The same for legacy messages addressed only by queue name.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_queue_lease_name_idx
    ON invocations (app_id, queue_name, due_at, created_at, id)
    WHERE source = 'queue' AND state = 'pending' AND queue_binding_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_queue_lease_name_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_queue_lease_binding_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_drain_due_idx;
-- +goose StatementEnd
