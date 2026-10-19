-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A retained confirmation is distinct from current assignment state. Retrying
-- an older successful change must never undo a more recent assignment.
CREATE TABLE IF NOT EXISTS product_control_customer_changes (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
    created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS product_control_recent_runs ON product_control_receipts(account_id, app_id, tenant_id, period, admitted_at DESC, operation_id DESC);

-- +goose Down
DROP INDEX product_control_recent_runs;
DROP TABLE product_control_customer_changes;
