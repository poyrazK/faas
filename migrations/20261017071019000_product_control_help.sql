-- +goose Up
CREATE TABLE product_control_help_requests (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    auth_app_id uuid NOT NULL CHECK (auth_app_id <> '00000000-0000-0000-0000-000000000000'),
    auth_organization_id uuid NOT NULL CHECK (auth_organization_id <> '00000000-0000-0000-0000-000000000000'),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    kind text NOT NULL CHECK (kind IN ('workspace', 'feature', 'run')),
    request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object' AND octet_length(request::text) <= 8192),
    context jsonb NOT NULL CHECK (jsonb_typeof(context) = 'object' AND octet_length(context::text) <= 8192),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'resolved')),
    response text NOT NULL DEFAULT '' CHECK (octet_length(response) <= 2000),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 1000),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    updated_at timestamptz NOT NULL CHECK (isfinite(updated_at) AND updated_at >= created_at)
);
CREATE INDEX product_control_help_owner_queue
ON product_control_help_requests(account_id, app_id, created_at DESC, id DESC);
CREATE INDEX product_control_help_customer_history
ON product_control_help_requests(account_id, app_id, tenant_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE product_control_help_requests;
