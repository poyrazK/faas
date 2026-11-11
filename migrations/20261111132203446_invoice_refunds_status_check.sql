-- filename: 20261111132203446_invoice_refunds_status_check.sql
-- migration-compat: expand
-- invoice_refunds.status had no CHECK (implicit state machines audit
-- 2026-11-08, finding 5). It stores the provider's normalized refund status
-- (Stripe, Polar, Paddle values such as pending, requires_action, succeeded,
-- failed, canceled, pending_approval), so the check bounds its shape rather
-- than listing values: a new provider status must not make refund webhooks
-- fail and retry. Empty stays valid for rows written before normalization
-- (it means succeeded). Added NOT VALID and validated separately so the
-- table is not locked against writes while existing rows are checked.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'invoice_refunds'::regclass
                    AND conname = 'invoice_refunds_status_check') THEN
    ALTER TABLE invoice_refunds
      ADD CONSTRAINT invoice_refunds_status_check CHECK (status ~ '^[a-z_-]{0,64}$') NOT VALID;
  END IF;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE invoice_refunds VALIDATE CONSTRAINT invoice_refunds_status_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE invoice_refunds DROP CONSTRAINT IF EXISTS invoice_refunds_status_check;
-- +goose StatementEnd
