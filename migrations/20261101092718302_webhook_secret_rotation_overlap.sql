-- filename: 20261101092718302_webhook_secret_rotation_overlap.sql
-- migration-compat: expand

-- ADR-20261101093517: a webhook secret rotation keeps the secret it replaces
-- for 24 hours. Outbound subscriptions (app, account release and platform
-- tenant webhooks) sign deliveries with both; inbound endpoints accept
-- signatures made with either. The secret is sealed like the current one, to
-- the same seal domain. Constant defaults make every column metadata-only; an
-- older apid or schedd ignores them and keeps the single-secret behaviour.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE app_webhooks ADD COLUMN IF NOT EXISTS previous_secret_sealed bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE app_webhooks ADD COLUMN IF NOT EXISTS previous_secret_expires_at timestamptz;
ALTER TABLE inbound_webhook_endpoints ADD COLUMN IF NOT EXISTS previous_signing_secret_sealed bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE inbound_webhook_endpoints ADD COLUMN IF NOT EXISTS previous_signing_secret_expires_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE inbound_webhook_endpoints DROP COLUMN IF EXISTS previous_signing_secret_expires_at;
ALTER TABLE inbound_webhook_endpoints DROP COLUMN IF EXISTS previous_signing_secret_sealed;
ALTER TABLE app_webhooks DROP COLUMN IF EXISTS previous_secret_expires_at;
ALTER TABLE app_webhooks DROP COLUMN IF EXISTS previous_secret_sealed;
