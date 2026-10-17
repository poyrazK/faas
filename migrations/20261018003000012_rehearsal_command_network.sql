-- +goose Up
CREATE TABLE scenario_command_networks (
 task_id uuid PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
 node_id uuid NOT NULL REFERENCES compute_nodes(id),
 host_ip inet NOT NULL CHECK (family(host_ip)=4 AND host_ip << '10.0.0.0/8'::inet),
 lease_token uuid NOT NULL,
 active boolean NOT NULL DEFAULT true
);
CREATE INDEX scenario_command_network_address ON scenario_command_networks(node_id,host_ip) WHERE active;
-- +goose StatementBegin
CREATE FUNCTION scenario_command_network_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NOT NEW.active OR NOT EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_recorder_sessions r ON r.instance_id=t.id AND r.app_id=t.app_id
   WHERE t.id=NEW.task_id AND t.lease_token=NEW.lease_token AND t.lease_expires_at>clock_timestamp()
    AND t.status='restoring' AND t.cancel_requested_at IS NULL AND r.status='recording'
    AND t.command[1]='__gregale_rehearsal_command_v1__') THEN
   RAISE EXCEPTION 'command network identity requires the restoring owner' USING ERRCODE='23514';
  END IF;
 ELSIF (NEW.task_id,NEW.node_id,NEW.host_ip,NEW.lease_token) IS DISTINCT FROM (OLD.task_id,OLD.node_id,OLD.host_ip,OLD.lease_token)
    OR NOT OLD.active OR NEW.active THEN
  RAISE EXCEPTION 'command network identity is immutable and retirement irreversible' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_command_network_fence BEFORE INSERT OR UPDATE ON scenario_command_networks FOR EACH ROW EXECUTE FUNCTION scenario_command_network_fence();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_command_networks) THEN
  RAISE EXCEPTION 'cannot remove retained command network authority';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_command_networks;
DROP FUNCTION scenario_command_network_fence();
