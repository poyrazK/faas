-- +goose Up
CREATE TABLE product_control_plan_changes (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at))
);
CREATE INDEX product_control_plan_changes_history
ON product_control_plan_changes(account_id, app_id, tenant_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE product_control_plan_changes;
