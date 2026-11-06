-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep admission intent with the durable stage projection. Capture direct SQL
-- and N-1 inserts, and restore the member if an older stage writer drops it.
-- Existing active rows capture OLD before a failure clears traffic/rollout.
-- Terminal legacy rows have already lost that information and are not guessed.
-- +goose StatementBegin
CREATE FUNCTION preserve_deployment_retry_intent() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE intent jsonb;
BEGIN
 IF TG_OP='INSERT' THEN
  intent:=jsonb_build_object('version','v1','traffic_percent',NEW.traffic_percent,
   'traffic_percent_explicit',NEW.traffic_percent_explicit,
   'service_rollout',NEW.canary_total_steps=0 AND coalesce(NEW.rollout_state,'pending')='rolling_out');
 ELSE
  intent:=OLD.stage_state->'retry_intent';
  IF intent IS NULL AND OLD.status NOT IN ('failed','cancelled','superseded') THEN
   intent:=jsonb_build_object('version','v1','traffic_percent',OLD.traffic_percent,
    'traffic_percent_explicit',OLD.traffic_percent_explicit,
    'service_rollout',OLD.canary_total_steps=0 AND coalesce(OLD.rollout_state,'pending')='rolling_out');
  END IF;
 END IF;
 IF intent IS NULL THEN
  NEW.stage_state:=coalesce(NEW.stage_state,'{}'::jsonb)-'retry_intent';
 ELSE
  NEW.stage_state:=jsonb_set(coalesce(NEW.stage_state,'{}'::jsonb),'{retry_intent}',intent,true);
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER deployments_retry_intent
BEFORE INSERT OR UPDATE OF status, stage_state ON deployments
FOR EACH ROW EXECUTE FUNCTION preserve_deployment_retry_intent();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER deployments_retry_intent ON deployments;
DROP FUNCTION preserve_deployment_retry_intent();
-- Retain the harmless JSON member so a rollback never discards retry intent.
