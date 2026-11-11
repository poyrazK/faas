-- migration-compat: expand
-- filename: 20261111160000418_managed_postgres_cap_events_egress_held.sql
--
-- ADR-20261110160000 amendment: the cap sweep records the month's egress hold
-- as cap event threshold 'egress_held', so its email is mailed once like the
-- compute thresholds. The threshold CHECK is widened in three steps, each its
-- own transaction: the wider constraint is added NOT VALID under a new name,
-- validated (every existing row passes), and then the narrower one is
-- dropped. The previous release writes only values both constraints allow,
-- and never writes 'egress_held'. Every step is re-runnable.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $widen$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'managed_postgres_cap_events'::regclass
      AND conname = 'managed_postgres_cap_events_threshold_v2_check'
  ) THEN
    ALTER TABLE managed_postgres_cap_events
      ADD CONSTRAINT managed_postgres_cap_events_threshold_v2_check
      CHECK (threshold IN ('warning', 'allowance', 'ceiling_warning', 'suspended', 'egress_held')) NOT VALID;
  END IF;
END
$widen$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE managed_postgres_cap_events VALIDATE CONSTRAINT managed_postgres_cap_events_threshold_v2_check;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE managed_postgres_cap_events DROP CONSTRAINT IF EXISTS managed_postgres_cap_events_threshold_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
DELETE FROM managed_postgres_cap_events WHERE threshold = 'egress_held';
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $narrow$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'managed_postgres_cap_events'::regclass
      AND conname = 'managed_postgres_cap_events_threshold_check'
  ) THEN
    ALTER TABLE managed_postgres_cap_events
      ADD CONSTRAINT managed_postgres_cap_events_threshold_check
      CHECK (threshold IN ('warning', 'allowance', 'ceiling_warning', 'suspended')) NOT VALID;
  END IF;
END
$narrow$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE managed_postgres_cap_events DROP CONSTRAINT IF EXISTS managed_postgres_cap_events_threshold_v2_check;
-- +goose StatementEnd
