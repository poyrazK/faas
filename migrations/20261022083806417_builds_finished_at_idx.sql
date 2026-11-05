-- filename: 20261022083806417_builds_finished_at_idx.sql
-- migration-compat: expand
-- ADR-20261018083536 slice C: apid's log projector pages finished builds by
-- (finished_at, id) every ten seconds to write build events into the
-- log_events ledger. No existing index orders builds by finish time, so
-- without this one each tick would scan the table.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS builds_finished_at_idx ON builds (finished_at, id)
    WHERE finished_at IS NOT NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS builds_finished_at_idx;
RESET lock_timeout;
