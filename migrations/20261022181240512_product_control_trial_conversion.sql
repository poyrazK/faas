-- filename: 20261022181240512_product_control_trial_conversion.sql

-- ADR-20261022180921: a Product Control customer records when a payment
-- method was last seen on file and when their ended trial was resolved: kept
-- on its plan, or moved to the plan's fallback. Only a trial can be
-- resolved.
-- migration-compat: expand
--
-- The columns are added without a default, which writes no rows. The CHECK is
-- added NOT VALID and validated in its own statement, so customers keep being
-- written during the scan. The resolution step's index is built
-- concurrently. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE product_control_customers ADD COLUMN IF NOT EXISTS payment_method_at timestamptz;

ALTER TABLE product_control_customers ADD COLUMN IF NOT EXISTS trial_resolved_at timestamptz;

-- +goose StatementBegin
DO $conversion$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'product_control_customers'::regclass AND conname = 'product_control_customers_conversion_chk') THEN
    ALTER TABLE product_control_customers ADD CONSTRAINT product_control_customers_conversion_chk
      CHECK ((trial_resolved_at IS NULL OR trial_ends_at IS NOT NULL) AND (payment_method_at IS NULL OR isfinite(payment_method_at))) NOT VALID;
  END IF;
END
$conversion$;
-- +goose StatementEnd

ALTER TABLE product_control_customers VALIDATE CONSTRAINT product_control_customers_conversion_chk;

CREATE INDEX CONCURRENTLY IF NOT EXISTS product_control_customers_unresolved_trials_idx ON product_control_customers (trial_ends_at)
  WHERE trial_ends_at IS NOT NULL AND trial_resolved_at IS NULL;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DROP INDEX CONCURRENTLY IF EXISTS product_control_customers_unresolved_trials_idx;

ALTER TABLE product_control_customers DROP CONSTRAINT IF EXISTS product_control_customers_conversion_chk;

ALTER TABLE product_control_customers DROP COLUMN IF EXISTS trial_resolved_at;

ALTER TABLE product_control_customers DROP COLUMN IF EXISTS payment_method_at;

RESET lock_timeout;
