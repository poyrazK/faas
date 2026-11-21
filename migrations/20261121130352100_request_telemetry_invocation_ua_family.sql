-- filename: 20261121130352100_request_telemetry_invocation_ua_family.sql
-- migration-compat: expand
-- The gateway records platform-delivered invocations (cron, queue,
-- async_invoke, delayed_task, ...) with client family "gregale-<source>"
-- (#7428, prod findings 2026-11-15 #7), but the ua_family CHECK never allowed
-- those values: every such row failed with db_error, and the gateway dropped
-- its batch after three retries (prod 2026-11-21: ~100 batches a day). A new
-- CHECK admits one family per invocations.source value. Widening only: every
-- existing row passes. Replaced in three steps (add NOT VALID, validate, drop
-- the old one) so no step blocks telemetry writes while it scans.

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    SET LOCAL lock_timeout = '5s';
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'request_telemetry'::regclass
          AND conname = 'request_telemetry_client_family_check'
    ) THEN
        ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_client_family_check CHECK (ua_family IN (
            'chrome', 'edge', 'firefox', 'safari', 'opera', 'curl', 'wget',
            'python', 'go', 'java', 'bot', 'other', '__unknown__',
            'gregale-async_invoke', 'gregale-exclusive_operation', 'gregale-inbound_webhook',
            'gregale-queue', 'gregale-delayed_task', 'gregale-cron', 'gregale-replay'
        )) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE request_telemetry VALIDATE CONSTRAINT request_telemetry_client_family_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    SET LOCAL lock_timeout = '5s';
    ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_ua_family_check;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    SET LOCAL lock_timeout = '5s';
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'request_telemetry'::regclass
          AND conname = 'request_telemetry_ua_family_check'
    ) THEN
        ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_ua_family_check CHECK (ua_family IN (
            'chrome', 'edge', 'firefox', 'safari', 'opera', 'curl', 'wget',
            'python', 'go', 'java', 'bot', 'other', '__unknown__'
        )) NOT VALID;
    END IF;
    ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_client_family_check;
END $$;
-- +goose StatementEnd
