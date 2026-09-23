-- filename: 20260923092725391_instances_draining_state.sql

-- +goose Up
-- +goose StatementBegin
-- A draining runtime-config predecessor is no longer routable, but its VM
-- remains resident and may still serve requests selected before the route
-- handoff. Keep it in all app/node/billing live sets until destruction.
ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_state_check;
ALTER TABLE instances
  ADD CONSTRAINT instances_state_check
  CHECK (state IN (
    'pending',
    'parked',
    'waking',
    'cold_booting',
    'running',
    'draining',
    'snapshotting',
    'migrating',
    'warm',
    'stopped',
    'failed',
    'evicting_account_deleting'
  )) NOT VALID;
ALTER TABLE instances VALIDATE CONSTRAINT instances_state_check;

DROP INDEX IF EXISTS instances_live_node_id_idx;
CREATE INDEX instances_live_node_id_idx
  ON instances (node_id)
  WHERE state IN ('waking', 'cold_booting', 'running', 'draining', 'warm');

DROP INDEX IF EXISTS instances_reaper_state_idx;
CREATE INDEX instances_reaper_state_idx
  ON instances (started_at DESC)
  WHERE state IN ('running', 'waking', 'cold_booting', 'snapshotting', 'warm', 'draining');

DROP INDEX IF EXISTS instances_app_deployment_idx;
CREATE INDEX instances_app_deployment_idx
  ON instances (deployment_id, app_id)
  WHERE state IN ('waking', 'cold_booting', 'running', 'draining');

CREATE OR REPLACE FUNCTION capture_instance_billing_interval()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
  old_billable boolean := false;
  new_billable boolean;
  changed_at timestamptz := clock_timestamp();
BEGIN
  new_billable := NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
                  AND COALESCE(NEW.mode, 'normal') <> 'mirror';
  IF TG_OP = 'INSERT' THEN
    old_billable := false;
  ELSE
    old_billable := OLD.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
                    AND COALESCE(OLD.mode, 'normal') <> 'mirror';
  END IF;

  IF new_billable AND NOT old_billable THEN
    INSERT INTO instance_billing_intervals (instance_id, started_at)
    VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN COALESCE(NEW.started_at, changed_at) ELSE changed_at END)
    ON CONFLICT (instance_id) WHERE ended_at IS NULL DO NOTHING;
  ELSIF old_billable AND NOT new_billable THEN
    UPDATE instance_billing_intervals
       SET ended_at = changed_at
     WHERE instance_id = NEW.id AND ended_at IS NULL;
  END IF;
  RETURN NEW;
END
$function$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Drain all instances before rolling back the state constraint.
ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_state_check;
ALTER TABLE instances
  ADD CONSTRAINT instances_state_check
  CHECK (state IN (
    'pending',
    'parked',
    'waking',
    'cold_booting',
    'running',
    'snapshotting',
    'migrating',
    'warm',
    'stopped',
    'failed',
    'evicting_account_deleting'
  )) NOT VALID;
ALTER TABLE instances VALIDATE CONSTRAINT instances_state_check;

DROP INDEX IF EXISTS instances_live_node_id_idx;
CREATE INDEX instances_live_node_id_idx
  ON instances (node_id)
  WHERE state IN ('waking', 'cold_booting', 'running', 'warm');
DROP INDEX IF EXISTS instances_reaper_state_idx;
CREATE INDEX instances_reaper_state_idx
  ON instances (started_at DESC)
  WHERE state IN ('running', 'waking', 'cold_booting', 'snapshotting', 'warm');
DROP INDEX IF EXISTS instances_app_deployment_idx;
CREATE INDEX instances_app_deployment_idx
  ON instances (deployment_id, app_id)
  WHERE state IN ('waking', 'cold_booting', 'running');

CREATE OR REPLACE FUNCTION capture_instance_billing_interval()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
  old_billable boolean := false;
  new_billable boolean;
  changed_at timestamptz := clock_timestamp();
BEGIN
  new_billable := NEW.state IN ('waking','cold_booting','running','snapshotting','migrating','warm')
                  AND COALESCE(NEW.mode, 'normal') <> 'mirror';
  IF TG_OP = 'INSERT' THEN
    old_billable := false;
  ELSE
    old_billable := OLD.state IN ('waking','cold_booting','running','snapshotting','migrating','warm')
                    AND COALESCE(OLD.mode, 'normal') <> 'mirror';
  END IF;

  IF new_billable AND NOT old_billable THEN
    INSERT INTO instance_billing_intervals (instance_id, started_at)
    VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN COALESCE(NEW.started_at, changed_at) ELSE changed_at END)
    ON CONFLICT (instance_id) WHERE ended_at IS NULL DO NOTHING;
  ELSIF old_billable AND NOT new_billable THEN
    UPDATE instance_billing_intervals
       SET ended_at = changed_at
     WHERE instance_id = NEW.id AND ended_at IS NULL;
  END IF;
  RETURN NEW;
END
$function$;
-- +goose StatementEnd
