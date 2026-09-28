-- filename: 20260928200059596_platform_tenant_offboarding_receipts.sql

-- +goose Up
-- +goose StatementBegin
-- Successful confirmed offboarding leaves a secret-free outcome that an
-- operator can recover after losing the synchronous response.
CREATE TABLE IF NOT EXISTS platform_tenant_offboarding_receipts (
    receipt_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    plan_hash text NOT NULL CHECK (plan_hash ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT now(),
    actions jsonb NOT NULL CHECK (jsonb_typeof(actions) = 'object'),
    FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS platform_tenant_offboarding_receipts_history_idx
    ON platform_tenant_offboarding_receipts (account_id, tenant_id, applied_at DESC, receipt_id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_offboarding_receipts;
-- +goose StatementEnd
