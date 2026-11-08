-- filename: 20261107105125431_billing_delivery_usage_source.sql

-- ADR-20261107105126 (ledger as billing source).
-- usage_source on the pusher's delivery receipts: which source quantified
-- the hour, usage_minutes or the financial ledger. meterd's usage source
-- switch can bill from the ledger; the receipt keeps which one billed each
-- hour for audit and disputes. NULL is every receipt written before this
-- column, all of them from usage_minutes. No backfill.
-- migration-compat: expand
-- Nullable columns without a default are catalog-only changes, and the CHECKs
-- are added NOT VALID and validated separately (SHARE UPDATE EXCLUSIVE), so
-- both tables keep being written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE billing_usage_deliveries ADD COLUMN IF NOT EXISTS usage_source text;
ALTER TABLE billing_meter_usage_deliveries ADD COLUMN IF NOT EXISTS usage_source text;

-- +goose StatementBegin
DO $usage_source$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_usage_deliveries'::regclass AND conname = 'billing_usage_deliveries_usage_source_chk') THEN
    ALTER TABLE billing_usage_deliveries ADD CONSTRAINT billing_usage_deliveries_usage_source_chk
      CHECK (usage_source IS NULL OR usage_source IN ('usage_minutes', 'ledger')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_meter_usage_deliveries'::regclass AND conname = 'billing_meter_usage_deliveries_usage_source_chk') THEN
    ALTER TABLE billing_meter_usage_deliveries ADD CONSTRAINT billing_meter_usage_deliveries_usage_source_chk
      CHECK (usage_source IS NULL OR usage_source IN ('usage_minutes', 'ledger')) NOT VALID;
  END IF;
END
$usage_source$;
-- +goose StatementEnd

ALTER TABLE billing_usage_deliveries VALIDATE CONSTRAINT billing_usage_deliveries_usage_source_chk;
ALTER TABLE billing_meter_usage_deliveries VALIDATE CONSTRAINT billing_meter_usage_deliveries_usage_source_chk;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE billing_meter_usage_deliveries DROP CONSTRAINT IF EXISTS billing_meter_usage_deliveries_usage_source_chk;
ALTER TABLE billing_usage_deliveries DROP CONSTRAINT IF EXISTS billing_usage_deliveries_usage_source_chk;
ALTER TABLE billing_meter_usage_deliveries DROP COLUMN IF EXISTS usage_source;
ALTER TABLE billing_usage_deliveries DROP COLUMN IF EXISTS usage_source;
