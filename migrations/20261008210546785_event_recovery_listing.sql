-- +goose Up
CREATE INDEX event_recovery_jobs_app_created_idx ON event_recovery_jobs(account_id,app_id,created_at DESC,id DESC);

-- +goose Down
DROP INDEX event_recovery_jobs_app_created_idx;
