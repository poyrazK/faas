-- filename: 20261116095310256_app_protection.sql
-- migration-compat: expand
-- Production protection by default (ADR-20261116120000), slice 1.
--
-- app_protection records an app's enrollment in the protection baseline:
-- 'on' when the app was created while the protection_baseline flag was on
-- for its account (or its owner opted in), 'off' when the owner turned it
-- off. No row means the app was never enrolled and keeps today's behaviour.
--
-- app_protection_objects records each object the baseline created for the
-- app (an SLO, a synthetic check, a notification channel, an alert rule or
-- a release gate) and the values it wrote, so the reconciler can tell an
-- object the customer edited or deleted from one it still owns. One row per
-- (app, item). Both tables are new; nothing reads them yet on older apid.

-- +goose Up
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_protection (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    state text NOT NULL CHECK (state IN ('on', 'off')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS app_protection_account_idx
    ON app_protection (account_id);

CREATE TABLE IF NOT EXISTS app_protection_objects (
    app_id uuid NOT NULL REFERENCES app_protection(app_id) ON DELETE CASCADE,
    item text NOT NULL CHECK (item IN (
        'availability_slo', 'latency_slo', 'health_check', 'email_channel',
        'availability_burn_alert', 'latency_burn_alert', 'health_alert',
        'availability_gate')),
    object_id text NOT NULL CHECK (object_id <> '' AND length(object_id) <= 128),
    written jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(written) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, item)
);

-- +goose Down
SET lock_timeout = '5s';

DROP TABLE IF EXISTS app_protection_objects;

DROP TABLE IF EXISTS app_protection;
