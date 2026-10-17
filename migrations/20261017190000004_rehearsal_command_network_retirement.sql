-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION scenario_command_retirement_network_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.command_result IS NOT NULL AND NOT EXISTS(SELECT 1 FROM scenario_command_networks
    WHERE task_id=NEW.instance_id AND NOT active) THEN
  RAISE EXCEPTION 'command retirement requires revoked network authority' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_command_retirement_network_fence BEFORE INSERT OR UPDATE ON scenario_recorder_sessions FOR EACH ROW EXECUTE FUNCTION scenario_command_retirement_network_fence();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot relax command network retirement fencing';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_command_retirement_network_fence ON scenario_recorder_sessions;
DROP FUNCTION scenario_command_retirement_network_fence();
