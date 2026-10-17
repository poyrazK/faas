-- filename: 20261015090953259_statement_invoice_automatic_tax.sql

-- ADR-1313: a Stripe billing connection can have Stripe Tax calculate tax
-- on exported invoices; each export records the setting it began with.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE account_billing_connections ADD COLUMN IF NOT EXISTS automatic_tax boolean NOT NULL DEFAULT false;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_billing_connections_automatic_tax_chk') THEN
    ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_automatic_tax_chk
      CHECK (NOT automatic_tax OR provider = 'stripe');
  END IF;
END $$;

ALTER TABLE usage_statement_invoice_exports ADD COLUMN IF NOT EXISTS automatic_tax boolean NOT NULL DEFAULT false;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_automatic_tax_chk') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_automatic_tax_chk
      CHECK (NOT automatic_tax OR provider = 'stripe');
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_automatic_tax_chk;
ALTER TABLE usage_statement_invoice_exports DROP COLUMN IF EXISTS automatic_tax;
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_automatic_tax_chk;
ALTER TABLE account_billing_connections DROP COLUMN IF EXISTS automatic_tax;
-- +goose StatementEnd
