-- +goose Up
CREATE INDEX IF NOT EXISTS event_execution_lineage_retry_jobs_health_idx
 ON event_execution_lineage_retry_jobs(subscription_id,retain_until,updated_at,request_id)
 WHERE state IN ('pending','paused');
-- +goose Down
DROP INDEX event_execution_lineage_retry_jobs_health_idx;
