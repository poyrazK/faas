-- filename: 20261123105120100_managed_postgres_history_billing.sql
-- migration-compat: expand
--
-- ADR-20261123062111: restore history and snapshots are billed on the
-- overage step. The usage meter CHECK gains snapshot_byte_seconds, replaced
-- online as in 20261121153413553 (widened copy NOT VALID, validate, swap).
-- Delivery and reservation rows gain history columns (default 0, CHECKs
-- NOT VALID); the previous release never names them. Steps are guarded.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_snapshot_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_check';
 IF base IS NULL OR position('''snapshot_byte_seconds''' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE managed_postgres_usage ADD CONSTRAINT managed_postgres_usage_meter_snapshot_chk CHECK ((%s) OR meter IN (%L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'snapshot_byte_seconds');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_snapshot_chk') THEN
  ALTER TABLE managed_postgres_usage VALIDATE CONSTRAINT managed_postgres_usage_meter_snapshot_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_snapshot_chk') THEN
  ALTER TABLE managed_postgres_usage DROP CONSTRAINT IF EXISTS managed_postgres_usage_meter_check;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_check') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_snapshot_chk';
 EXECUTE 'ALTER TABLE managed_postgres_usage ADD CONSTRAINT managed_postgres_usage_meter_check ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE managed_postgres_usage VALIDATE CONSTRAINT managed_postgres_usage_meter_check;
ALTER TABLE managed_postgres_usage DROP CONSTRAINT IF EXISTS managed_postgres_usage_meter_snapshot_chk;
ALTER TABLE managed_postgres_overage_deliveries
    ADD COLUMN IF NOT EXISTS billed_history_byte_seconds bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS billed_snapshot_byte_seconds bigint NOT NULL DEFAULT 0;
ALTER TABLE managed_postgres_overage_pending
    ADD COLUMN IF NOT EXISTS used_history_byte_seconds bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS used_snapshot_byte_seconds bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS included_history_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS billed_history_byte_seconds bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS billed_snapshot_byte_seconds bigint NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_overage_deliveries'::regclass
   AND conname = 'managed_postgres_overage_deliveries_billed_history_check') THEN
  ALTER TABLE managed_postgres_overage_deliveries ADD CONSTRAINT managed_postgres_overage_deliveries_billed_history_check
   CHECK (billed_history_byte_seconds >= 0 AND billed_snapshot_byte_seconds >= 0) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_overage_pending'::regclass
   AND conname = 'managed_postgres_overage_pending_history_check') THEN
  ALTER TABLE managed_postgres_overage_pending ADD CONSTRAINT managed_postgres_overage_pending_history_check
   CHECK (used_history_byte_seconds >= 0 AND used_snapshot_byte_seconds >= 0 AND included_history_bytes >= 0
     AND billed_history_byte_seconds >= 0 AND billed_snapshot_byte_seconds >= 0) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE managed_postgres_overage_pending
    DROP CONSTRAINT IF EXISTS managed_postgres_overage_pending_history_check,
    DROP COLUMN IF EXISTS used_history_byte_seconds,
    DROP COLUMN IF EXISTS used_snapshot_byte_seconds,
    DROP COLUMN IF EXISTS included_history_bytes,
    DROP COLUMN IF EXISTS billed_history_byte_seconds,
    DROP COLUMN IF EXISTS billed_snapshot_byte_seconds;
ALTER TABLE managed_postgres_overage_deliveries
    DROP CONSTRAINT IF EXISTS managed_postgres_overage_deliveries_billed_history_check,
    DROP COLUMN IF EXISTS billed_history_byte_seconds,
    DROP COLUMN IF EXISTS billed_snapshot_byte_seconds;
-- +goose StatementBegin
DO $$ DECLARE meter_def text;
BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_usage WHERE meter = 'snapshot_byte_seconds') THEN
  RAISE EXCEPTION 'delete snapshot_byte_seconds usage rows before rollback';
 END IF;
 SELECT pg_get_constraintdef(oid) INTO meter_def FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_meter_check';
 IF position(' OR (meter = ''snapshot_byte_seconds''::text)' in meter_def) > 0 THEN
  ALTER TABLE managed_postgres_usage DROP CONSTRAINT managed_postgres_usage_meter_check;
  EXECUTE 'ALTER TABLE managed_postgres_usage ADD CONSTRAINT managed_postgres_usage_meter_check ' || replace(meter_def, ' OR (meter = ''snapshot_byte_seconds''::text)', '');
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
