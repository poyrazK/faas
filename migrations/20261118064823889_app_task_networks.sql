-- filename: 20261118064823889_app_task_networks.sql
-- migration-compat: expand
-- ADR-20261118070000: an app task VM (`gregale app exec`, `gregale task run`)
-- calls the app's services as the app. schedd records the task VM's host
-- address here once vmmd restores it, and gatewayd-internal's caller lookup
-- reads it beside live instances. A row is identity only while its task holds
-- the lease that recorded it and runs; retirement is one-way.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_task_networks (
 task_id uuid PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
 node_id uuid NOT NULL REFERENCES compute_nodes(id),
 host_ip inet NOT NULL CHECK (family(host_ip)=4 AND host_ip << '10.0.0.0/8'::inet),
 lease_token uuid NOT NULL,
 active boolean NOT NULL DEFAULT true
);
CREATE INDEX IF NOT EXISTS app_task_network_address ON app_task_networks(node_id,host_ip) WHERE active;
COMMENT ON TABLE app_task_networks IS
 'ADR-20261118070000 service-mesh caller identity of a running app task VM, written by schedd.';

-- Only the task's restoring lease owner records an address, and a row
-- changes only by retiring. Rehearsal commands keep their own contained
-- identity in scenario_command_networks.
CREATE OR REPLACE FUNCTION app_task_network_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NOT NEW.active OR NOT EXISTS(SELECT 1 FROM app_tasks t
   WHERE t.id=NEW.task_id AND t.lease_token=NEW.lease_token AND t.lease_expires_at>clock_timestamp()
    AND t.status='restoring' AND t.cancel_requested_at IS NULL
    AND t.command[1] IS DISTINCT FROM '__gregale_rehearsal_command_v1__') THEN
   RAISE EXCEPTION 'app task network identity requires the restoring owner' USING ERRCODE='23514';
  END IF;
 ELSIF (NEW.task_id,NEW.node_id,NEW.host_ip,NEW.lease_token) IS DISTINCT FROM (OLD.task_id,OLD.node_id,OLD.host_ip,OLD.lease_token)
    OR NOT OLD.active OR NEW.active THEN
  RAISE EXCEPTION 'app task network identity is immutable and retirement irreversible' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS app_task_network_fence ON app_task_networks;
CREATE TRIGGER app_task_network_fence BEFORE INSERT OR UPDATE ON app_task_networks
 FOR EACH ROW EXECUTE FUNCTION app_task_network_fence();
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TABLE IF EXISTS app_task_networks;
DROP FUNCTION IF EXISTS app_task_network_fence();
-- +goose StatementEnd
