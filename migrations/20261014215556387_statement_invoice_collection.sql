-- filename: 20261014215556387_statement_invoice_collection.sql

-- ADR-1311: a billing connection can have its provider collect exported
-- invoices: none (drafts, the default), send (the provider issues and
-- emails the invoice) or charge (the provider charges the customer's saved
-- payment method). Each export records the mode it began with.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE account_billing_connections ADD COLUMN IF NOT EXISTS collection text NOT NULL DEFAULT 'none';
ALTER TABLE account_billing_connections ADD COLUMN IF NOT EXISTS days_until_due integer NOT NULL DEFAULT 30;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_billing_connections_collection_chk') THEN
    ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_collection_chk
      CHECK ((provider = 'stripe' AND collection IN ('none', 'send', 'charge'))
      OR (provider = 'paddle' AND collection = 'none')
      OR (provider = 'lago' AND collection IN ('none', 'charge')));
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_billing_connections_days_until_due_chk') THEN
    ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_days_until_due_chk
      CHECK (days_until_due BETWEEN 1 AND 365);
  END IF;
END $$;

ALTER TABLE usage_statement_invoice_exports ADD COLUMN IF NOT EXISTS collection text NOT NULL DEFAULT 'none';
ALTER TABLE usage_statement_invoice_exports ADD COLUMN IF NOT EXISTS days_until_due integer NOT NULL DEFAULT 30;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_collection_chk') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_collection_chk
      CHECK (collection IN ('none', 'send', 'charge') AND days_until_due BETWEEN 1 AND 365);
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_collection_chk;
ALTER TABLE usage_statement_invoice_exports DROP COLUMN IF EXISTS days_until_due;
ALTER TABLE usage_statement_invoice_exports DROP COLUMN IF EXISTS collection;
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_days_until_due_chk;
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_collection_chk;
ALTER TABLE account_billing_connections DROP COLUMN IF EXISTS days_until_due;
ALTER TABLE account_billing_connections DROP COLUMN IF EXISTS collection;
-- +goose StatementEnd
