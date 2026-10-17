-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE FUNCTION scenario_rehearsal_command_update_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.command[1] IS DISTINCT FROM '__gregale_rehearsal_command_v1__'
  AND NEW.command[1] IS DISTINCT FROM '__gregale_rehearsal_command_v1__'
  AND NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=OLD.app_id AND network_mode='contained') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.account_id,NEW.app_id,NEW.deployment_id,NEW.kind,NEW.command,NEW.command_shell,
     NEW.timeout_seconds,NEW.max_output_bytes,NEW.artifact_key,NEW.image_digest,NEW.deployment_scope)
  IS DISTINCT FROM
    (OLD.id,OLD.account_id,OLD.app_id,OLD.deployment_id,OLD.kind,OLD.command,OLD.command_shell,
     OLD.timeout_seconds,OLD.max_output_bytes,OLD.artifact_key,OLD.image_digest,OLD.deployment_scope) THEN
  RAISE EXCEPTION 'contained command intent is immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.status='succeeded' AND (NEW.output_truncated OR NOT EXISTS(
  SELECT 1 FROM scenario_recorder_sessions r WHERE r.instance_id=NEW.id AND r.app_id=NEW.app_id
   AND r.deployment_id=NEW.deployment_id AND r.status='closed' AND r.command_result=NEW.stdout_tail)) THEN
  RAISE EXCEPTION 'contained command success requires its owner retirement receipt' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_rehearsal_command_update_fence BEFORE UPDATE ON app_tasks FOR EACH ROW EXECUTE FUNCTION scenario_rehearsal_command_update_fence();

-- +goose StatementBegin
CREATE FUNCTION scenario_rehearsal_command_attach_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM app_tasks t WHERE t.id=NEW.task_id AND
  (t.command[1]='__gregale_rehearsal_command_v1__' OR EXISTS(SELECT 1 FROM scenario_test_action_policies p WHERE p.app_id=t.app_id AND p.network_mode='contained'))) THEN
  RAISE EXCEPTION 'contained commands cannot attach an interactive client' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_rehearsal_command_attach_fence BEFORE INSERT ON app_task_attach_sessions FOR EACH ROW EXECUTE FUNCTION scenario_rehearsal_command_attach_fence();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot relax command receipt fencing while simulation enrollment remains';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_rehearsal_command_attach_fence ON app_task_attach_sessions;
DROP FUNCTION scenario_rehearsal_command_attach_fence();
DROP TRIGGER scenario_rehearsal_command_update_fence ON app_tasks;
DROP FUNCTION scenario_rehearsal_command_update_fence();
