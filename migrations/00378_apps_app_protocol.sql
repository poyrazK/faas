-- filename: 00378_apps_app_protocol.sql
-- +goose Up
-- Per-app wire-protocol selector (ADR-124). Lets the customer pick
-- http1, http2, or grpc at the public edge. Default 'http1' keeps every
-- pre-existing app on the legacy H1 path — opt-in by the customer via
-- PATCH /v1/apps/{slug} or manifest. http2 is universal; grpc is
-- Hobby/Pro/Scale only via aplan gate enforced in apid, NOT a column-
-- level CHECK (the gate is plan-tiered, the closed set is universal).
--
-- One column:
--   app_protocol  text NOT NULL DEFAULT 'http1'
-- One CHECK:
--   apps_app_protocol_chk  IN ('http1', 'http2', 'grpc')
--
-- Default 'http1' is the load-bearing behavioral backstop: every
-- pre-existing app sees the column land with 'http1' and the proxy
-- path takes the legacy branch the platform already runs today.
-- Zero behavior change for every current customer.
--
-- Replay-safe (PR #377 / ADR-041): ADD COLUMN IF NOT EXISTS plus
-- DROP CONSTRAINT IF EXISTS / ADD CONSTRAINT pattern (mirrors the
-- 00346_deployments_annotation.sql:54-60 closed-set CHECK precedent).
-- The column is a single NOT NULL text with a constant default — no
-- rewrite, no index bloat, and a second MigrateUp is a no-op.
-- +goose StatementBegin
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS app_protocol text NOT NULL DEFAULT 'http1';

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_app_protocol_chk;
ALTER TABLE apps
    ADD  CONSTRAINT apps_app_protocol_chk
        CHECK (app_protocol IN ('http1', 'http2', 'grpc'));
-- +goose StatementEnd

-- +goose Down
-- Reverse: drop the constraint, then drop the column. A row that had
-- app_protocol='http2' or 'grpc' loses the framing marker on downgrade;
-- the GET /v1/apps/{slug} response shape omits app_protocol because the
-- column no longer exists, which is the correct degraded behaviour
-- (the proxy falls back to the legacy H1 path on every app).
-- +goose StatementBegin
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_app_protocol_chk;
ALTER TABLE apps
    DROP COLUMN IF EXISTS app_protocol;
-- +goose StatementEnd
