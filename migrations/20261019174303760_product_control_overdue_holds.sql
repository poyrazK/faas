-- filename: 20261019174303760_product_control_overdue_holds.sql

-- ADR-20261018212918: an app can refuse new Product Control starts from a
-- customer whose exported statement is still unpaid a number of days after
-- its due date, mirroring ADR-1315. apid recomputes the holds; Operations
-- admission reads a customer's hold under its existing locks.
-- migration-compat: expand
--
-- Each statement is its own transaction so none locks more than three
-- existing tables: a hold's statement and export foreign keys are added after
-- the table, each guarded so every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS product_control_overdue_policies (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    block_overdue_after_days integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_control_overdue_policies_days_chk CHECK (block_overdue_after_days BETWEEN 0 AND 365)
);
COMMENT ON TABLE product_control_overdue_policies IS
  'Per-app rule refusing new Product Control starts from customers whose exported statement is unpaid that many days after its due date (ADR-20261018212918).';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS product_control_overdue_holds (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    document_id uuid NOT NULL,
    export_id uuid NOT NULL,
    due_at timestamptz NOT NULL,
    since timestamptz NOT NULL,
    PRIMARY KEY (app_id, tenant_id)
);
CREATE INDEX IF NOT EXISTS product_control_overdue_holds_app_idx ON product_control_overdue_holds (account_id, app_id, due_at);
COMMENT ON TABLE product_control_overdue_holds IS
  'Product Control customers whose new starts are refused because of an overdue invoice; recomputed by apid (ADR-20261018212918).';
-- +goose StatementEnd

-- +goose StatementBegin
DO $holds$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'product_control_overdue_holds'::regclass
                 AND conname = 'product_control_overdue_holds_document_id_fkey') THEN
    ALTER TABLE product_control_overdue_holds ADD CONSTRAINT product_control_overdue_holds_document_id_fkey
      FOREIGN KEY (document_id) REFERENCES product_control_billing_documents(id) ON DELETE CASCADE;
  END IF;
END
$holds$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $holds$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'product_control_overdue_holds'::regclass
                 AND conname = 'product_control_overdue_holds_export_id_fkey') THEN
    ALTER TABLE product_control_overdue_holds ADD CONSTRAINT product_control_overdue_holds_export_id_fkey
      FOREIGN KEY (export_id) REFERENCES usage_statement_invoice_exports(id) ON DELETE CASCADE;
  END IF;
END
$holds$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DROP TABLE IF EXISTS product_control_overdue_holds;

DROP TABLE IF EXISTS product_control_overdue_policies;

RESET lock_timeout;
