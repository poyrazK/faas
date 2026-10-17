-- +goose Up
CREATE TABLE IF NOT EXISTS product_control_email_subscriptions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
 auth_app_id uuid NOT NULL CHECK(auth_app_id<>'00000000-0000-0000-0000-000000000000'),
 organization_id uuid NOT NULL CHECK(organization_id<>'00000000-0000-0000-0000-000000000000'),
 actor_id uuid NOT NULL CHECK(actor_id<>'00000000-0000-0000-0000-000000000000'),
 app_slug text NOT NULL CHECK(octet_length(app_slug) BETWEEN 1 AND 63),
 auth_slug text NOT NULL CHECK(octet_length(auth_slug) BETWEEN 1 AND 63),
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 1000000),
 work_outcomes boolean NOT NULL,
 help_replies boolean NOT NULL,
 work_since timestamptz NOT NULL CHECK(isfinite(work_since)),
 help_since timestamptz NOT NULL CHECK(isfinite(help_since)),
 expires_at timestamptz,
 grant_sealed bytea NOT NULL CHECK(octet_length(grant_sealed)<=4096),
 scan_cursor text NOT NULL CHECK(octet_length(scan_cursor)<=1024),
 CHECK((work_outcomes OR help_replies) = (expires_at IS NOT NULL)),
 CHECK(expires_at IS NULL OR (isfinite(expires_at) AND octet_length(grant_sealed)>0)),
 UNIQUE(account_id,app_id,tenant_id,auth_app_id,organization_id,actor_id)
);
CREATE TABLE IF NOT EXISTS product_control_email_deliveries (
 id uuid PRIMARY KEY,
 subscription_id uuid NOT NULL REFERENCES product_control_email_subscriptions(id) ON DELETE CASCADE,
 preference_version bigint NOT NULL CHECK(preference_version BETWEEN 1 AND 1000000),
 source_revision text NOT NULL CHECK(source_revision ~ '^[a-f0-9]{64}$'),
 notification jsonb NOT NULL CHECK(jsonb_typeof(notification)='object'),
 status text NOT NULL CHECK(status IN ('queued','leased','captured','provider_accepted','failed','cancelled')),
 reason text NOT NULL CHECK(reason IN ('','access_changed','preferences_changed','source_changed','authorization_unavailable','transport_unavailable','provider_rejected','retry_exhausted','expired')),
 attempts integer NOT NULL CHECK(attempts BETWEEN 0 AND 6),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at)),
 updated_at timestamptz NOT NULL CHECK(isfinite(updated_at)),
 next_at timestamptz NOT NULL CHECK(isfinite(next_at)),
 deadline timestamptz NOT NULL CHECK(isfinite(deadline) AND deadline>created_at),
 UNIQUE(subscription_id,source_revision)
);
CREATE INDEX IF NOT EXISTS product_control_email_subscribers ON product_control_email_subscriptions(id) WHERE work_outcomes OR help_replies;
CREATE INDEX IF NOT EXISTS product_control_email_due ON product_control_email_deliveries(next_at,id) WHERE status IN ('queued','leased');
CREATE INDEX IF NOT EXISTS product_control_email_recent ON product_control_email_deliveries(subscription_id,created_at DESC,id DESC);

CREATE INDEX IF NOT EXISTS product_control_email_expiry ON product_control_email_deliveries(created_at,id);

-- +goose Down
DROP TABLE product_control_email_deliveries;
DROP TABLE product_control_email_subscriptions;
