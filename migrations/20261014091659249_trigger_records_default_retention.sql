-- migration-compat: expand
-- Built concurrently: trigger_records grows with every broker record, so a
-- plain build would block dispatch writes for its whole duration. Replacing an
-- interrupted build also removes the invalid index PostgreSQL leaves behind.
-- +goose NO TRANSACTION

-- +goose Up
-- Terminal trigger_records without an explicit result_retention_until are
-- deleted after a default age (pkg/api TriggerRecordRetention and
-- TriggerDeadLetterRetention) when schedd runs with
-- FAAS_TRIGGER_LEDGER_RETENTION_ENABLED=1. This partial index keeps that sweep
-- to the terminal, unpinned rows ordered by age. Work-policy records are
-- excluded: their lanes keep explicit retention only.
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS trigger_records_terminal_age_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS trigger_records_terminal_age_idx
    ON trigger_records ((coalesce(last_dispatched_at, received_at)))
    WHERE result_retention_until IS NULL AND work_policy_name IS NULL
      AND state IN ('succeeded','superseded','cancelled','expired','dead_letter');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS trigger_records_terminal_age_idx;
RESET lock_timeout;
