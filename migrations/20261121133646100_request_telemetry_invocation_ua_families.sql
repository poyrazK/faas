-- filename: 20261121133646100_request_telemetry_invocation_ua_families.sql
-- migration-compat: expand
-- #7428 labels a platform-delivered invocation's request telemetry row with
-- the client family "gregale-<source>" (cron, queue, ...), but the column's
-- CHECK still allowed only browser and tool families. On prod every cron and
-- queue delivery's row failed the insert from 2026-11-20 19:30Z; the gateway
-- then dropped the whole batch, customer requests with it, and reported its
-- telemetry unhealthy, which held every health-gated canary at its first step
-- (prod hunt 2026-11-21). The CHECK now also allows the invocation families.
-- It only widens: N-1 writers never send a value it refuses. The wider CHECK
-- is added NOT VALID under a new name, validated (SHARE UPDATE EXCLUSIVE),
-- and only then is the narrow one dropped, each its own statement. Every
-- step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $ua_family$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_telemetry'::regclass AND conname = 'request_telemetry_ua_family_chk') THEN
    ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_ua_family_chk CHECK (ua_family IN (
      'chrome', 'edge', 'firefox', 'safari', 'opera', 'curl', 'wget',
      'python', 'go', 'java', 'bot', 'other', '__unknown__',
      'gregale-async_invoke', 'gregale-exclusive_operation', 'gregale-inbound_webhook',
      'gregale-queue', 'gregale-delayed_task', 'gregale-cron', 'gregale-replay'
    )) NOT VALID;
  END IF;
END
$ua_family$;
-- +goose StatementEnd

ALTER TABLE request_telemetry VALIDATE CONSTRAINT request_telemetry_ua_family_chk;

ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_ua_family_check;

-- +goose Down
-- Narrowing would refuse rows this release wrote; the wider CHECK stays.
SELECT 1;
