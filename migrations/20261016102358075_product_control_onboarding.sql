-- +goose Up
CREATE TABLE IF NOT EXISTS product_control_onboardings (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    -- The identity snapshot survives retirement/deletion of the Auth app.
    auth_app_id uuid NOT NULL CHECK (auth_app_id <> '00000000-0000-0000-0000-000000000000'),
    auth_organization_id uuid NOT NULL CHECK (auth_organization_id <> '00000000-0000-0000-0000-000000000000'),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
    created_at timestamptz NOT NULL,
    access_verified_at timestamptz CHECK (access_verified_at IS NULL OR access_verified_at >= created_at)
);
CREATE INDEX IF NOT EXISTS product_control_onboarding_customer ON product_control_onboardings(account_id,app_id,tenant_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS product_control_onboarding_success ON product_control_receipts(account_id,app_id,tenant_id,resolved_at,operation_id) WHERE resolved_state='succeeded';

-- +goose Down
DROP INDEX product_control_onboarding_success;
DROP TABLE product_control_onboardings;
