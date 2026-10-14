-- +goose Up
-- Terminal trigger_records without an explicit result_retention_until are
-- deleted after a default age (pkg/api TriggerRecordRetention and
-- TriggerDeadLetterRetention). This partial index keeps that sweep to the
-- terminal, unpinned rows ordered by age. Work-policy records are excluded:
-- their lanes keep explicit retention only. Non-CONCURRENTLY for the same
-- reason as 00069: goose runs each file in a transaction.
CREATE INDEX IF NOT EXISTS trigger_records_terminal_age_idx
    ON trigger_records ((coalesce(last_dispatched_at, received_at)))
    WHERE result_retention_until IS NULL AND work_policy_name IS NULL
      AND state IN ('succeeded','superseded','cancelled','expired','dead_letter');

-- +goose Down
DROP INDEX IF EXISTS trigger_records_terminal_age_idx;
