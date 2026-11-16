-- filename: 20261116095310256_app_protection.sql
-- migration-compat: expand
-- Production protection by default (ADR-20261116120000).
--
-- app_protection records an app's enrollment in the protection baseline:
-- 'on' when the app was created while the protection_baseline flag was on
-- for its account (or its owner opted in), 'off' when the owner turned it
-- off. No row means the app was never enrolled and keeps today's behaviour.
-- next_reconcile_at and lease_until schedule the apid reconciler that
-- creates the baseline objects: one apid replica claims an app at a time.
-- health_probe_at is when apid last claimed the app's baseline health
-- check: apid, not meterd, probes that check through the route-probe
-- challenge, so the probe writes no telemetry or usage.
--
-- app_protection_objects records each baseline item: 'created' with the
-- object the baseline made and the values it wrote, or 'skipped' with the
-- reason it could not be made yet (plan, capability, unverified email, a
-- name the customer already uses, a full quota). A created item is never
-- made again, so an object the customer deletes stays deleted. One row per
-- (app, item). Both tables are new; nothing reads them on older apid.

-- +goose Up
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_protection (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    state text NOT NULL CHECK (state IN ('on', 'off')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    next_reconcile_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    health_probe_at timestamptz
);

CREATE INDEX IF NOT EXISTS app_protection_account_idx
    ON app_protection (account_id);

CREATE INDEX IF NOT EXISTS app_protection_due_idx
    ON app_protection (next_reconcile_at) WHERE state = 'on';

CREATE TABLE IF NOT EXISTS app_protection_objects (
    app_id uuid NOT NULL REFERENCES app_protection(app_id) ON DELETE CASCADE,
    item text NOT NULL CHECK (item IN (
        'availability_slo', 'latency_slo', 'health_check', 'email_channel',
        'availability_burn_alert', 'latency_burn_alert', 'health_alert',
        'availability_gate')),
    status text NOT NULL DEFAULT 'created' CHECK (status IN ('created', 'skipped')),
    object_id text NOT NULL DEFAULT '' CHECK (length(object_id) <= 128),
    reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 64),
    written jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(written) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, item),
    CONSTRAINT app_protection_objects_status_chk CHECK (
        (status = 'created' AND object_id <> '' AND reason = '')
        OR (status = 'skipped' AND object_id = '' AND reason <> ''))
);

-- +goose Down
SET lock_timeout = '5s';

DROP TABLE IF EXISTS app_protection_objects;

DROP TABLE IF EXISTS app_protection;
