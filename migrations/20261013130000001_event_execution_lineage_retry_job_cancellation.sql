-- +goose Up
ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT event_execution_lineage_retry_jobs_state_check;
ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','completed','cancelled'));

-- +goose Down
-- Retained cancelled rows deliberately block downgrade; expire/prune them first.
-- Never change a cancelled job back to pending as part of a downgrade.
ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT event_execution_lineage_retry_jobs_state_check;
ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','completed'));
