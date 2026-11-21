-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_workflow_pipelines
 ADD COLUMN IF NOT EXISTS completion jsonb,
 ADD COLUMN IF NOT EXISTS delivered_at timestamptz;
-- Completion and its outbox are committed together. N-1 workers may continue
-- ordinary pipelines but cannot erase a callback pipeline without its manifest.
-- +goose StatementBegin
DO $guard$
BEGIN
 IF to_regprocedure('execution_pipeline_completion_fence()') IS NULL THEN
CREATE FUNCTION execution_pipeline_completion_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p execution_workflow_pipelines;
BEGIN
 IF NEW.status IN ('succeeded','failed') AND OLD.status IS DISTINCT FROM NEW.status THEN
  SELECT * INTO p FROM execution_workflow_pipelines WHERE job_id=NEW.id;
  IF FOUND THEN
   IF (p.policy ? 'callback' OR p.policy->>'completion_version'='v1') AND p.completion IS NULL THEN
    RAISE EXCEPTION 'pipeline completion writer required' USING ERRCODE='23514';
   END IF;
   IF p.completion IS NOT NULL THEN
    INSERT INTO app_webhook_deliveries(webhook_id,app_id,account_id,event,payload,source_event_id)
    SELECT w.id,NULL,NEW.account_id,'execution.pipeline.finished',jsonb_build_object(
     'event_id',NEW.id,'workflow_id',NEW.workflow_id,'outcome',p.completion->>'outcome',
     'finished_at',p.completion->>'finished_at','receipt_url','/v1/execution-workflows/'||NEW.workflow_id),NEW.id
    FROM app_webhooks w WHERE w.account_id=NEW.account_id AND w.scope='account' AND w.enabled AND 'execution.pipeline.finished'=ANY(w.event_filter)
    ON CONFLICT(source_event_id,webhook_id) WHERE source_event_id IS NOT NULL DO NOTHING;
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
 END IF;
END $guard$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'agent_execution_workflows'::regclass AND tgname = 'execution_pipeline_completion_fence') THEN
  CREATE TRIGGER execution_pipeline_completion_fence BEFORE UPDATE OF status ON agent_execution_workflows FOR EACH ROW EXECUTE FUNCTION execution_pipeline_completion_fence();
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
