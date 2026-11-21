-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_workflow_pipelines ADD CONSTRAINT execution_pipeline_completion_chk
 CHECK (completion IS NULL OR (jsonb_typeof(completion)='object' AND octet_length(completion::text)<=1048576)) NOT VALID;
-- +goose Down
SELECT 1;
