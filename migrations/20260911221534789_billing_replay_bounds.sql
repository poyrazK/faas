-- filename: 20260911221534789_billing_replay_bounds.sql

-- +goose Up
-- +goose StatementBegin
-- Existing provider identities retain access to historical usage so a deploy
-- does not strand a real outage backlog. New identities start at creation time,
-- preventing checkout and provider switches from rebilling older usage.
ALTER TABLE billing_identities
  ADD COLUMN IF NOT EXISTS billing_from timestamptz;

UPDATE billing_identities
   SET billing_from = '1970-01-01T00:00:00Z'
 WHERE billing_from IS NULL;

ALTER TABLE billing_identities
  ALTER COLUMN billing_from SET DEFAULT now(),
  ALTER COLUMN billing_from SET NOT NULL;

-- Pending refunds are reservations, not completed money movement. Keeping a
-- separate aggregate preserves the over-refund guard while invoice history
-- reports only settled refunds.
ALTER TABLE invoices
  ADD COLUMN IF NOT EXISTS amount_refund_pending_cents bigint NOT NULL DEFAULT 0;

ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_amount_refund_pending_cents_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_amount_refund_pending_cents_check
  CHECK (amount_refund_pending_cents >= 0);
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_refund_totals_within_paid_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_refund_totals_within_paid_check
  CHECK (amount_refunded_cents + amount_refund_pending_cents <= greatest(amount_paid_cents, total_cents));

ALTER TABLE invoice_refunds
  ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

-- Failed asynchronous credit refunds restore the original credit balance with
-- an append-only compensating ledger row. The consumption uniqueness rule is
-- scoped to negative rows so one positive reversal can share the invoice key.
ALTER TABLE credit_ledger
  ADD COLUMN IF NOT EXISTS refund_reversal_id uuid REFERENCES invoice_refunds(id) ON DELETE SET NULL;

DROP INDEX IF EXISTS credit_ledger_invoice_credit_idx;
CREATE UNIQUE INDEX credit_ledger_invoice_credit_idx
  ON credit_ledger (provider_invoice_id, credit_id)
  WHERE provider_invoice_id IS NOT NULL AND delta_cents < 0;

CREATE UNIQUE INDEX IF NOT EXISTS credit_ledger_refund_reversal_idx
  ON credit_ledger (refund_reversal_id, credit_id)
  WHERE refund_reversal_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE billing_identities
  DROP COLUMN IF EXISTS billing_from;
ALTER TABLE invoice_refunds
  DROP COLUMN IF EXISTS updated_at;
DROP INDEX IF EXISTS credit_ledger_refund_reversal_idx;
UPDATE credit_ledger SET provider_invoice_id = NULL WHERE delta_cents > 0 AND provider_invoice_id IS NOT NULL;
DROP INDEX IF EXISTS credit_ledger_invoice_credit_idx;
CREATE UNIQUE INDEX credit_ledger_invoice_credit_idx
  ON credit_ledger (provider_invoice_id, credit_id)
  WHERE provider_invoice_id IS NOT NULL;
ALTER TABLE credit_ledger
  DROP COLUMN IF EXISTS refund_reversal_id;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_amount_refund_pending_cents_check;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_refund_totals_within_paid_check;
ALTER TABLE invoices
  DROP COLUMN IF EXISTS amount_refund_pending_cents;
-- +goose StatementEnd
