-- filename: 20261018184954302_statement_invoice_exports_product_control.sql

-- ADR-20261018212918: finalized Product Control statements export to the
-- account's own Stripe, Paddle or Lago like other statements. statement_id
-- is the Product Control statement document ID.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_source_chk;
ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_source_chk
  CHECK (source_kind IN ('app_consumer', 'platform_tenant', 'product_control'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM usage_statement_invoice_exports WHERE source_kind = 'product_control';
ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_source_chk;
ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_source_chk
  CHECK (source_kind IN ('app_consumer', 'platform_tenant'));
-- +goose StatementEnd
