-- +goose Up
CREATE INDEX IF NOT EXISTS event_execution_lineage_retry_jobs_listing_idx
 ON event_execution_lineage_retry_jobs(subscription_id,created_at DESC,request_id DESC);
-- +goose Down
DROP INDEX IF EXISTS event_execution_lineage_retry_jobs_listing_idx;
