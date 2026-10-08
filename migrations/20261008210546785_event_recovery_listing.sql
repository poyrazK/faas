-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS event_recovery_jobs_app_created_idx ON event_recovery_jobs(account_id,app_id,created_at DESC,id DESC);
-- +goose StatementEnd

-- +goose Down
DROP INDEX event_recovery_jobs_app_created_idx;
