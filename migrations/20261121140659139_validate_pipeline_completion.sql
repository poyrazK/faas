-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_workflow_pipelines VALIDATE CONSTRAINT execution_pipeline_completion_chk;
-- +goose Down
SELECT 1;
