-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_command_networks') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_command_network_address') AND relkind IN ('i','I'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_command_networks') AND tgname='scenario_command_network_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_command_network_fence()') AND md5(prosrc)=ANY(ARRAY['2bf25cc00844806537b849749f44ce57'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_command_networks') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_command_network_address') AND relkind IN ('i','I'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_command_networks') AND tgname='scenario_command_network_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_command_network_fence()') AND md5(prosrc)=ANY(ARRAY['2bf25cc00844806537b849749f44ce57'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 12; inspect recovery before continuing';
  END IF;

CREATE TABLE scenario_command_networks (
 task_id uuid PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
 node_id uuid NOT NULL REFERENCES compute_nodes(id),
 host_ip inet NOT NULL CHECK (family(host_ip)=4 AND host_ip << '10.0.0.0/8'::inet),
 lease_token uuid NOT NULL,
 active boolean NOT NULL DEFAULT true
);
CREATE INDEX scenario_command_network_address ON scenario_command_networks(node_id,host_ip) WHERE active;
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
CREATE TRIGGER scenario_command_network_fence BEFORE INSERT OR UPDATE ON scenario_command_networks FOR EACH ROW EXECUTE FUNCTION scenario_command_network_fence();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_command_networks) THEN
  RAISE EXCEPTION 'cannot remove retained command network authority';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_command_networks;
DROP FUNCTION scenario_command_network_fence();
