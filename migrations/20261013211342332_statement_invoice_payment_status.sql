-- filename: 20261013211342332_statement_invoice_payment_status.sql

-- ADR-1302: follow each exported Stripe invoice until it is paid, voided or
-- deleted, and record which billing runs and overdue invoices the account was
-- told about.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE usage_statement_invoice_exports
  ADD COLUMN IF NOT EXISTS payment_status         text,
  ADD COLUMN IF NOT EXISTS amount_paid_minor      bigint,
  ADD COLUMN IF NOT EXISTS amount_remaining_minor bigint,
  ADD COLUMN IF NOT EXISTS due_at                 timestamptz,
  ADD COLUMN IF NOT EXISTS paid_at                timestamptz,
  ADD COLUMN IF NOT EXISTS hosted_invoice_url     text,
  ADD COLUMN IF NOT EXISTS status_synced_at       timestamptz,
  ADD COLUMN IF NOT EXISTS next_status_sync_at    timestamptz,
  ADD COLUMN IF NOT EXISTS overdue_notified_at    timestamptz;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_payment_status_chk') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_payment_status_chk
      CHECK (payment_status IS NULL OR payment_status IN
      ('draft', 'open', 'paid', 'void', 'uncollectible', 'deleted'));
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_payment_amounts_chk') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_payment_amounts_chk
      CHECK (
      (amount_paid_minor IS NULL OR amount_paid_minor >= 0) AND (amount_remaining_minor IS NULL OR amount_remaining_minor >= 0));
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_hosted_url_chk') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_hosted_url_chk
      CHECK (
      hosted_invoice_url IS NULL OR (char_length(hosted_invoice_url) <= 2048 AND hosted_invoice_url LIKE 'https://%'));
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS usage_statement_invoice_exports_sync_idx
  ON usage_statement_invoice_exports (next_status_sync_at) WHERE next_status_sync_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS usage_statement_invoice_exports_overdue_idx
  ON usage_statement_invoice_exports (due_at)
  WHERE payment_status = 'open' AND overdue_notified_at IS NULL;

ALTER TABLE billing_runs ADD COLUMN IF NOT EXISTS notified_at timestamptz;
CREATE INDEX IF NOT EXISTS billing_runs_notify_idx
  ON billing_runs (completed_at) WHERE status = 'completed' AND notified_at IS NULL;

COMMENT ON COLUMN usage_statement_invoice_exports.payment_status IS
  'Stripe invoice status last read by apid (ADR-1302); NULL until the first sync.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS billing_runs_notify_idx;
ALTER TABLE billing_runs DROP COLUMN IF EXISTS notified_at;
DROP INDEX IF EXISTS usage_statement_invoice_exports_overdue_idx;
DROP INDEX IF EXISTS usage_statement_invoice_exports_sync_idx;
ALTER TABLE usage_statement_invoice_exports
  DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_hosted_url_chk,
  DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_payment_amounts_chk,
  DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_payment_status_chk,
  DROP COLUMN IF EXISTS overdue_notified_at,
  DROP COLUMN IF EXISTS next_status_sync_at,
  DROP COLUMN IF EXISTS status_synced_at,
  DROP COLUMN IF EXISTS hosted_invoice_url,
  DROP COLUMN IF EXISTS paid_at,
  DROP COLUMN IF EXISTS due_at,
  DROP COLUMN IF EXISTS amount_remaining_minor,
  DROP COLUMN IF EXISTS amount_paid_minor,
  DROP COLUMN IF EXISTS payment_status;
-- +goose StatementEnd
