-- Keep the unified failed-events projection bounded without touching source
-- rows or append-only audit history. The scheduler deletes oldest projections
-- by last_failed_at in bounded batches, including account-owned job/workflow
-- rows whose app_id is NULL.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS dead_letter_events_retention_idx
    ON dead_letter_events (last_failed_at ASC, id ASC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS dead_letter_events_retention_idx;
-- +goose StatementEnd
