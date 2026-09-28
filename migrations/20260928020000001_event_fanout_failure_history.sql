-- +goose Up
-- Recipient failures always retain last_error on the outbox row. This partial
-- index keeps app-scoped failure history reads away from successful receipts.
CREATE INDEX IF NOT EXISTS event_fanout_failure_history_idx
    ON event_fanout_outbox (account_id, created_at DESC, id DESC)
    WHERE last_error IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS event_fanout_failure_history_idx;
