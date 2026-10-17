-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS product_control_revisions (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    revision text NOT NULL,
    spec jsonb NOT NULL,
    PRIMARY KEY (app_id, revision)
);
CREATE TABLE IF NOT EXISTS product_control_heads (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    revision text NOT NULL,
    FOREIGN KEY (app_id, revision) REFERENCES product_control_revisions(app_id, revision)
);
CREATE TABLE IF NOT EXISTS product_control_customers (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    revision text NOT NULL,
    plan text NOT NULL,
    suspended boolean NOT NULL DEFAULT false,
    PRIMARY KEY (app_id, tenant_id),
    FOREIGN KEY (app_id, revision) REFERENCES product_control_revisions(app_id, revision)
);
-- Independent of operation result/event retention. One immutable admission per
-- logical operation; settlement only records its first verified success.
CREATE TABLE IF NOT EXISTS product_control_receipts (
    operation_id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    revision text NOT NULL,
    feature text NOT NULL,
    plan text NOT NULL,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    period text NOT NULL CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
    unit_price_millicents bigint NOT NULL CHECK (unit_price_millicents BETWEEN 0 AND 1000000000),
    admitted_at timestamptz NOT NULL,
    succeeded_at timestamptz,
    FOREIGN KEY (app_id, revision) REFERENCES product_control_revisions(app_id, revision)
);
CREATE INDEX IF NOT EXISTS product_control_quota ON product_control_receipts(app_id, tenant_id, feature, period);

-- +goose Down
DROP TABLE product_control_receipts;
DROP TABLE product_control_customers;
DROP TABLE product_control_heads;
DROP TABLE product_control_revisions;
