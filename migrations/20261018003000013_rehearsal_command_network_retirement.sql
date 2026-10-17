-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_recorder_sessions') AND tgname='scenario_command_retirement_network_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_command_retirement_network_fence()') AND md5(prosrc)=ANY(ARRAY['e98e1015076c34aae3062ecc8ffa4c26'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_recorder_sessions') AND tgname='scenario_command_retirement_network_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_command_retirement_network_fence()') AND md5(prosrc)=ANY(ARRAY['e98e1015076c34aae3062ecc8ffa4c26'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 13; inspect recovery before continuing';
  END IF;

CREATE FUNCTION scenario_command_retirement_network_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.command_result IS NOT NULL AND NOT EXISTS(SELECT 1 FROM scenario_command_networks
    WHERE task_id=NEW.instance_id AND NOT active) THEN
  RAISE EXCEPTION 'command retirement requires revoked network authority' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_command_retirement_network_fence BEFORE INSERT OR UPDATE ON scenario_recorder_sessions FOR EACH ROW EXECUTE FUNCTION scenario_command_retirement_network_fence();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot relax command network retirement fencing';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_command_retirement_network_fence ON scenario_recorder_sessions;
DROP FUNCTION scenario_command_retirement_network_fence();
