-- filename: 20261017143758915_platform_tenant_overdue_holds.sql

-- ADR-1324: an account's rule blocks platform tenants whose exported invoice
-- is unpaid that many days after its due date, as ADR-1315 does for app
-- consumers. Re-runnable.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS platform_tenant_billing_policies (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    block_overdue_after_days integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT platform_tenant_billing_policies_days_chk CHECK (block_overdue_after_days BETWEEN 0 AND 365)
);
COMMENT ON TABLE platform_tenant_billing_policies IS
  'Per-account rule blocking platform tenants whose exported invoice is unpaid that many days after its due date (ADR-1324).';

CREATE TABLE IF NOT EXISTS platform_tenant_overdue_holds (
    platform_tenant_id uuid PRIMARY KEY REFERENCES platform_tenants(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    export_id uuid NOT NULL REFERENCES usage_statement_invoice_exports(id) ON DELETE CASCADE,
    due_at timestamptz NOT NULL,
    since timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS platform_tenant_overdue_holds_account_idx ON platform_tenant_overdue_holds (account_id, due_at);
COMMENT ON TABLE platform_tenant_overdue_holds IS
  'Platform tenants the gateway refuses because of an overdue invoice; recomputed by apid (ADR-1324).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_overdue_holds;
DROP TABLE IF EXISTS platform_tenant_billing_policies;
-- +goose StatementEnd
