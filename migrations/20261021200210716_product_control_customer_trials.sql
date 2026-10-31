-- filename: 20261021200210716_product_control_customer_trials.sql

-- ADR-20261021195901: a Product Control customer has at most one free trial
-- in an app, [trial_starts_at, trial_ends_at), kept on their assignment.
-- Both columns are set together, and a trial ends after it starts.
-- migration-compat: expand
--
-- The columns are added without a default, which writes no rows. The CHECK is
-- added NOT VALID and validated in its own transaction, so customers keep
-- being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE product_control_customers ADD COLUMN IF NOT EXISTS trial_starts_at timestamptz;

ALTER TABLE product_control_customers ADD COLUMN IF NOT EXISTS trial_ends_at timestamptz;

-- +goose StatementBegin
DO $trial$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'product_control_customers'::regclass AND conname = 'product_control_customers_trial_chk') THEN
    ALTER TABLE product_control_customers ADD CONSTRAINT product_control_customers_trial_chk
      CHECK ((trial_starts_at IS NULL) = (trial_ends_at IS NULL) AND (trial_ends_at IS NULL OR trial_ends_at > trial_starts_at)) NOT VALID;
  END IF;
END
$trial$;
-- +goose StatementEnd

ALTER TABLE product_control_customers VALIDATE CONSTRAINT product_control_customers_trial_chk;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE product_control_customers DROP CONSTRAINT IF EXISTS product_control_customers_trial_chk;

ALTER TABLE product_control_customers DROP COLUMN IF EXISTS trial_ends_at;

ALTER TABLE product_control_customers DROP COLUMN IF EXISTS trial_starts_at;

RESET lock_timeout;
