-- +goose Up
-- +goose StatementBegin
-- ADR-211: the retention worker scans oldest events across all accounts.
-- App-scoped query indexes cannot serve an event-time-first sweep.
CREATE INDEX IF NOT EXISTS log_events_retention_time_idx
    ON log_events (occurred_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS log_events_retention_time_idx;
-- +goose StatementEnd
