-- filename: 20261008190746911_instances_mode_fork.sql
-- migration-compat: expand
--
-- ADR-732: widen instances.mode with 'fork', a quarantined, non-serving
-- production fork. Forks bill like any live instance (only 'mirror' is
-- skipped by capture_instance_billing_interval), are never routed, and are
-- excluded from max_concurrency by the scheduler's admission ledger.
--
-- The CHECK is replaced online and keeps its name: the wider list is added
-- NOT VALID under a temporary name and validated, the old one dropped, then
-- the same list is added back under the old name, validated, and the
-- temporary one dropped. Each step is its own transaction; a VALIDATE takes
-- SHARE UPDATE EXCLUSIVE, so instances keeps taking writes during the scans,
-- and a CHECK always holds. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $fork$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'instances'::regclass AND conname = 'instances_mode_check_v2') THEN
    ALTER TABLE instances ADD CONSTRAINT instances_mode_check_v2
      CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service', 'fork')) NOT VALID;
  END IF;
END
$fork$;
-- +goose StatementEnd

ALTER TABLE instances VALIDATE CONSTRAINT instances_mode_check_v2;

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check;

-- +goose StatementBegin
DO $fork$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'instances'::regclass AND conname = 'instances_mode_check') THEN
    ALTER TABLE instances ADD CONSTRAINT instances_mode_check
      CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service', 'fork')) NOT VALID;
  END IF;
END
$fork$;
-- +goose StatementEnd

ALTER TABLE instances VALIDATE CONSTRAINT instances_mode_check;

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check_v2;

RESET lock_timeout;

-- +goose Down
-- Fails while fork rows exist; schedd destroys forks at their TTL, so wait
-- for them to end (or stop them) before rolling back.
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $fork$
BEGIN
  ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check_v2;
  ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_mode_check;
  ALTER TABLE instances ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service'));
END
$fork$;
-- +goose StatementEnd

RESET lock_timeout;
