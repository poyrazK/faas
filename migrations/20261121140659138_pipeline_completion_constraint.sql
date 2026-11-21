-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'execution_workflow_pipelines'::regclass AND conname = 'execution_pipeline_completion_chk') THEN
  ALTER TABLE execution_workflow_pipelines ADD CONSTRAINT execution_pipeline_completion_chk
   CHECK (completion IS NULL OR (jsonb_typeof(completion)='object' AND octet_length(completion::text)<=1048576)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
