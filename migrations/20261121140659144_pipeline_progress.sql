-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_workflow_pipelines ADD COLUMN IF NOT EXISTS journal jsonb NOT NULL DEFAULT '{"sequence":0}'::jsonb
 CHECK(jsonb_typeof(journal)='object' AND octet_length(journal::text)<=262144);
-- +goose StatementBegin
DO $guard$
BEGIN
 IF to_regprocedure('execution_pipeline_progress_claim_fence()') IS NULL THEN
CREATE FUNCTION execution_pipeline_progress_claim_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='running' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token
 AND EXISTS(SELECT 1 FROM execution_workflow_pipelines p WHERE p.job_id=NEW.id AND p.policy->>'progress_version'='v1')
 AND current_setting('faas.run_pipeline_progress_protocol',true) IS DISTINCT FROM '1' THEN
 RETURN NULL;
 END IF;
 RETURN NEW;
END $$;
 END IF;
END $guard$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'agent_execution_workflows'::regclass AND tgname = 'execution_pipeline_progress_claim_fence') THEN
  CREATE TRIGGER execution_pipeline_progress_claim_fence BEFORE UPDATE ON agent_execution_workflows FOR EACH ROW EXECUTE FUNCTION execution_pipeline_progress_claim_fence();
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER execution_pipeline_progress_claim_fence ON agent_execution_workflows;
DROP FUNCTION execution_pipeline_progress_claim_fence();
ALTER TABLE execution_workflow_pipelines DROP COLUMN journal;
