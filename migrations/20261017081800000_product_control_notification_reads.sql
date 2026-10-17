-- +goose Up
CREATE TABLE IF NOT EXISTS product_control_notification_reads (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
 auth_app_id uuid NOT NULL CHECK(auth_app_id<>'00000000-0000-0000-0000-000000000000'),
 auth_organization_id uuid NOT NULL CHECK(auth_organization_id<>'00000000-0000-0000-0000-000000000000'),
 source_kind text NOT NULL CHECK(source_kind IN ('help','work')),
 source_id uuid NOT NULL CHECK(source_id<>'00000000-0000-0000-0000-000000000000'),
 source_version text NOT NULL CHECK(octet_length(source_version) BETWEEN 1 AND 128),
 is_read boolean NOT NULL,
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 1000000),
 updated_at timestamptz NOT NULL CHECK(isfinite(updated_at)),
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at) AND expires_at>updated_at),
 PRIMARY KEY(account_id,app_id,tenant_id,auth_app_id,auth_organization_id,source_kind,source_id)
);
CREATE INDEX IF NOT EXISTS product_control_notification_expiry ON product_control_notification_reads(account_id,app_id,tenant_id,expires_at);
CREATE INDEX IF NOT EXISTS product_control_notification_outcomes ON product_control_receipts(account_id,app_id,tenant_id,resolved_at DESC,operation_id DESC) WHERE resolved_state<>'';
CREATE INDEX IF NOT EXISTS product_control_notification_pending ON product_control_receipts(account_id,app_id,tenant_id,operation_id) WHERE resolved_state='';

-- +goose Down
DROP INDEX product_control_notification_pending;
DROP INDEX product_control_notification_outcomes;
DROP TABLE product_control_notification_reads;
