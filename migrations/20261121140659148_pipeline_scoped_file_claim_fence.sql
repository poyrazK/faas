-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $guard$
BEGIN
 IF to_regprocedure('execution_pipeline_scoped_file_claim_fence()') IS NULL THEN
CREATE FUNCTION execution_pipeline_scoped_file_claim_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='running' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token
 AND current_setting('faas.run_pipeline_scoped_file_protocol',true) IS DISTINCT FROM '1'
 AND EXISTS (
   SELECT 1 FROM execution_workflow_pipelines p
   WHERE p.job_id=NEW.id AND p.policy->>'scoped_file_inputs'='true'
 ) THEN
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
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'agent_execution_workflows'::regclass AND tgname = 'execution_pipeline_scoped_file_claim_fence') THEN
  CREATE TRIGGER execution_pipeline_scoped_file_claim_fence BEFORE UPDATE ON agent_execution_workflows
   FOR EACH ROW EXECUTE FUNCTION execution_pipeline_scoped_file_claim_fence();
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (
   SELECT 1 FROM execution_workflow_pipelines p JOIN agent_execution_workflows j ON j.id=p.job_id
   WHERE p.policy->>'scoped_file_inputs'='true' AND j.status IN ('queued','running')
 ) THEN
  RAISE EXCEPTION 'settle scoped-file Agent pipelines before removing their claim fence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER execution_pipeline_scoped_file_claim_fence ON agent_execution_workflows;
DROP FUNCTION execution_pipeline_scoped_file_claim_fence();
