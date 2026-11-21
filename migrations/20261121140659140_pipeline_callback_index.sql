-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS execution_pipeline_callbacks_pending;
CREATE INDEX CONCURRENTLY execution_pipeline_callbacks_pending ON execution_workflow_pipelines(job_id)
 WHERE policy ? 'callback' AND completion IS NOT NULL AND delivered_at IS NULL;
RESET lock_timeout;
-- +goose Down
SELECT 1;
