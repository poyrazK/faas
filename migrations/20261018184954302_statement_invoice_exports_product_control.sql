-- filename: 20261018184954302_statement_invoice_exports_product_control.sql

-- ADR-20261018212918: finalized Product Control statements export to the
-- account's own Stripe, Paddle or Lago like other statements. statement_id
-- is the Product Control statement document ID.
-- migration-compat: expand
--
-- The CHECK is replaced online and keeps its name: the wider list is added
-- NOT VALID under a temporary name and validated, the old one dropped, then
-- the same list is added back under the old name, validated, and the
-- temporary one dropped. Each step is its own transaction; a VALIDATE takes
-- SHARE UPDATE EXCLUSIVE, so exports keep being written during the scans, and
-- a CHECK always holds. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $source$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'usage_statement_invoice_exports'::regclass
                 AND conname = 'usage_statement_invoice_exports_source_chk_v2') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_source_chk_v2
      CHECK (source_kind IN ('app_consumer', 'platform_tenant', 'product_control')) NOT VALID;
  END IF;
END
$source$;
-- +goose StatementEnd

ALTER TABLE usage_statement_invoice_exports VALIDATE CONSTRAINT usage_statement_invoice_exports_source_chk_v2;

ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_source_chk;

-- +goose StatementBegin
DO $source$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'usage_statement_invoice_exports'::regclass
                 AND conname = 'usage_statement_invoice_exports_source_chk') THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_source_chk
      CHECK (source_kind IN ('app_consumer', 'platform_tenant', 'product_control')) NOT VALID;
  END IF;
END
$source$;
-- +goose StatementEnd

ALTER TABLE usage_statement_invoice_exports VALIDATE CONSTRAINT usage_statement_invoice_exports_source_chk;

ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_source_chk_v2;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM usage_statement_invoice_exports WHERE source_kind = 'product_control';

-- +goose StatementBegin
DO $source$
BEGIN
  ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_source_chk_v2;
  ALTER TABLE usage_statement_invoice_exports DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_source_chk;
  ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_source_chk
    CHECK (source_kind IN ('app_consumer', 'platform_tenant'));
END
$source$;
-- +goose StatementEnd

RESET lock_timeout;
