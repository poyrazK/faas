-- filename: 20261013221818550_billing_providers_paddle_lago.sql

-- ADR-1305: statement exports, billing runs and schedules also target
-- Paddle and Lago. A Lago connection stores its API URL.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE account_billing_connections ADD COLUMN IF NOT EXISTS api_url text;
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_provider_chk;
ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_provider_chk
  CHECK (provider IN ('stripe', 'paddle', 'lago'));
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_billing_connections_api_url_chk') THEN
    ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_api_url_chk
      CHECK (api_url IS NULL OR (char_length(api_url) <= 512 AND api_url LIKE 'https://%'));
  END IF;
END $$;

ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_provider_chk;
ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_provider_chk
  CHECK (provider IN ('stripe', 'paddle', 'lago'));

ALTER TABLE billing_runs DROP CONSTRAINT IF EXISTS billing_runs_export_chk;
ALTER TABLE billing_runs ADD CONSTRAINT billing_runs_export_chk
  CHECK (export_provider IN ('none', 'stripe', 'paddle', 'lago'));

ALTER TABLE billing_run_schedules DROP CONSTRAINT IF EXISTS billing_run_schedules_export_chk;
ALTER TABLE billing_run_schedules ADD CONSTRAINT billing_run_schedules_export_chk
  CHECK (export_provider IN ('none', 'stripe', 'paddle', 'lago'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE billing_run_schedules DROP CONSTRAINT IF EXISTS billing_run_schedules_export_chk;
ALTER TABLE billing_run_schedules ADD CONSTRAINT billing_run_schedules_export_chk CHECK (export_provider IN ('none', 'stripe'));
ALTER TABLE billing_runs DROP CONSTRAINT IF EXISTS billing_runs_export_chk;
ALTER TABLE billing_runs ADD CONSTRAINT billing_runs_export_chk CHECK (export_provider IN ('none', 'stripe'));
ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_provider_chk;
ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_provider_chk CHECK (provider IN ('stripe'));
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_api_url_chk;
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_provider_chk;
ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_provider_chk CHECK (provider IN ('stripe'));
ALTER TABLE account_billing_connections DROP COLUMN IF EXISTS api_url;
-- +goose StatementEnd
