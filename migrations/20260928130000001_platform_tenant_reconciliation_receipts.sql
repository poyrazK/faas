-- +goose Up
-- +goose StatementBegin
-- Successful confirmed applies leave a durable, secret-free receipt so a
-- platform can recover the result after losing the synchronous response.
CREATE TABLE IF NOT EXISTS platform_tenant_reconciliation_receipts (
    receipt_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    plan_hash text NOT NULL CHECK (plan_hash ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT now(),
    changes jsonb NOT NULL CHECK (jsonb_typeof(changes) = 'array'),
    FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS platform_tenant_reconciliation_receipts_history_idx
    ON platform_tenant_reconciliation_receipts (account_id, tenant_id, applied_at DESC, receipt_id DESC);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS platform_tenant_reconciliation_receipts;
