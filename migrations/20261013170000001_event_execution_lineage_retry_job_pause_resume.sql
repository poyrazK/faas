-- +goose Up
ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT event_execution_lineage_retry_jobs_state_check;
ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','paused','completed','cancelled'));

-- +goose Down
-- Retained paused rows block downgrade. Deliberately resume/cancel them or expire/prune first.
-- Never resume work implicitly as part of a downgrade.
ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT event_execution_lineage_retry_jobs_state_check;
ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','completed','cancelled'));
