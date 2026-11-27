-- filename: 20261121135135716_unbill_platform_snapshot_migrate.sql
-- migration-compat: expand
--
-- Account usage audit 2026-11-21: 'snapshotting' and 'migrating' are
-- platform work, not capacity the customer uses. An instance enters
-- 'snapshotting' only for the terminal capture of a park, after its idle
-- timeout and once it is no longer routable (sched snapshotAndPark), and
-- 'migrating' only while the platform moves it between nodes (drain,
-- rebalance, recovery). Both were billable in capture_instance_billing_interval,
-- so every park and every platform migration added seconds to the customer's
-- GB-hours. They now close the billing interval like 'parked'; a migration
-- that lands reopens it when the row returns to 'running'.
--
-- Only the trigger function changes: no table rewrite, no lock beyond the
-- function replace.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_instance_billing_interval() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  old_billable boolean := false;
  new_billable boolean;
  changed_at timestamptz := clock_timestamp();
BEGIN
  new_billable := NEW.state IN ('waking','cold_booting','running','draining','warm')
                  AND COALESCE(NEW.mode, 'normal') NOT IN ('mirror', 'refresh');
  IF TG_OP = 'INSERT' THEN
    old_billable := false;
  ELSE
    old_billable := OLD.state IN ('waking','cold_booting','running','draining','warm')
                    AND COALESCE(OLD.mode, 'normal') NOT IN ('mirror', 'refresh');
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
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_instance_billing_interval() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  old_billable boolean := false;
  new_billable boolean;
  changed_at timestamptz := clock_timestamp();
BEGIN
  new_billable := NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
                  AND COALESCE(NEW.mode, 'normal') NOT IN ('mirror', 'refresh');
  IF TG_OP = 'INSERT' THEN
    old_billable := false;
  ELSE
    old_billable := OLD.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
                    AND COALESCE(OLD.mode, 'normal') NOT IN ('mirror', 'refresh');
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
$$;
-- +goose StatementEnd
