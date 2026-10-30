-- filename: 20261029191848000_request_telemetry_platform_refusal.sql

-- ADR-20261028110044 amendment 3: a 5xx the gateway returned because a
-- Gregale dependency could not admit the request (node admission, policy,
-- rate-limit or revocation state unavailable) is marked so route health and
-- production monitoring do not count it against either deployment. A constant
-- default keeps the change metadata-only on every partition.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE request_telemetry ADD COLUMN IF NOT EXISTS platform_refusal boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE request_telemetry DROP COLUMN IF EXISTS platform_refusal;
