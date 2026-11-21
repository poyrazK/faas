-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $guard$
BEGIN
 IF to_regprocedure('enqueue_execution_finished_webhooks()') IS NULL THEN
CREATE FUNCTION enqueue_execution_finished_webhooks() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status IN ('succeeded','failed','timed_out','out_of_memory','cancelled') AND OLD.status IS DISTINCT FROM NEW.status THEN
  INSERT INTO app_webhook_deliveries(webhook_id,app_id,account_id,event,payload,source_event_id)
  SELECT w.id,NULL,NEW.account_id,'execution.finished',jsonb_strip_nulls(jsonb_build_object(
   'event_id',NEW.id,'execution_id',NEW.id,'status',NEW.status,'runtime',NEW.runtime,
   'finished_at',NEW.finished_at,'receipt_url','/v1/executions/'||NEW.id,
   'environment_id',NEW.environment_id,'workflow_id',NULLIF(NEW.workflow_id,''),
   'result_validation_status',NEW.result_validation->>'status')),NEW.id
  FROM app_webhooks w WHERE w.account_id=NEW.account_id AND w.scope='account' AND w.enabled AND 'execution.finished'=ANY(w.event_filter)
  ON CONFLICT(source_event_id,webhook_id) WHERE source_event_id IS NOT NULL DO NOTHING;
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
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'executions'::regclass AND tgname = 'execution_finished_webhooks') THEN
  CREATE TRIGGER execution_finished_webhooks AFTER UPDATE OF status ON executions FOR EACH ROW EXECUTE FUNCTION enqueue_execution_finished_webhooks();
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
