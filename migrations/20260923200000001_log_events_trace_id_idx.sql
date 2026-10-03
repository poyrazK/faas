-- +goose Up
-- +goose StatementBegin

-- Account trace lookup uses the tenant/app scope plus trace id and retention
-- window. Keep the access path narrow and exclude rows without correlation.
CREATE INDEX IF NOT EXISTS log_events_app_trace_time_idx
    ON log_events (account_id, app_id, trace_id, occurred_at DESC, id DESC)
    WHERE trace_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS log_events_app_trace_time_idx;
-- +goose StatementEnd
