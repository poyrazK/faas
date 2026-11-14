-- filename: 20261106221510798_instances_mode_refresh.sql
-- migration-compat: expand
--
-- ADR-20261106214916: widen instances.mode with 'refresh', a platform
-- snapshot refresh. After a node takes a release that made an app's snapshot
-- stale, schedd boots the app's current deployment, captures a fresh snapshot
-- and parks it before a user asks. A refresh is never routed and is not
-- billed: capture_instance_billing_interval skips it like 'mirror'.
--
-- The CHECK is replaced online and keeps its name, as the fork migration
-- (20261008190746911) did: the wider list is added NOT VALID under a
-- temporary name and validated, the old one dropped, then the same list is
-- added back under the old name, validated, and the temporary one dropped.
-- Each step is its own transaction; a VALIDATE takes SHARE UPDATE EXCLUSIVE,
-- so instances keeps taking writes during the scans, and a CHECK always
-- holds. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $refresh$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'instances'::regclass AND conname = 'instances_mode_check_v2') THEN
    ALTER TABLE instances ADD CONSTRAINT instances_mode_check_v2
      CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service', 'fork', 'refresh')) NOT VALID;
  END IF;
END
$refresh$;
-- +goose StatementEnd

ALTER TABLE instances VALIDATE CONSTRAINT instances_mode_check_v2;

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check;

-- +goose StatementBegin
DO $refresh$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'instances'::regclass AND conname = 'instances_mode_check') THEN
    ALTER TABLE instances ADD CONSTRAINT instances_mode_check
      CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service', 'fork', 'refresh')) NOT VALID;
  END IF;
END
$refresh$;
-- +goose StatementEnd

ALTER TABLE instances VALIDATE CONSTRAINT instances_mode_check;

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check_v2;

-- A refresh opens no billing interval, exactly like a mirror.
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

RESET lock_timeout;

-- +goose Down
-- Fails while refresh rows exist; a refresh parks or fails within its boot
-- budget, so wait for them to end before rolling back.
SET lock_timeout = '5s';

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
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $refresh$
BEGIN
  ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check_v2;
  ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check;
  ALTER TABLE instances ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service', 'fork'));
END
$refresh$;
-- +goose StatementEnd

RESET lock_timeout;
