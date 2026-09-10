-- filename: 00440_request_telemetry_count.sql
-- +goose Up
-- +goose StatementBegin

-- ADR-127 §PR-B — collapse (dedupe) support on request_telemetry.
--
-- PR-A shipped every request as a row. PR-B collapses by
-- (app_id, deployment_id, route, method, status, minute_bucket,
-- latency_bucket)
-- in pkg/gateway/request_telemetry_publisher.go::collapseRequestTelemetry
-- (the function that the no-op pass-through at :264-267 in PR-A
-- replaced with a real aggregate). The collapse writes one row per
-- bounded latency bucket with count = the number of original requests
-- that folded into it. Keeping latency buckets separate preserves
-- percentile signal without allowing one row per request.
--
-- Column semantics:
--   * count — INT NOT NULL DEFAULT 1. Default of 1 preserves
--     the PR-A semantic for any writer that hasn't been updated
--     to pass the collapsed count (publisher version skew during
--     rolling upgrade). The CHECK keeps a buggy writer from
--     persisting count=0 or negative.
--   * No DEFAULT collapse to __other__: the recorder still
--     inserts count=1 when no collapse has happened yet (e.g.
--     first request after a fresh gateway boot, or a single
--     request in a given minute).
--
-- Replay-safe posture: ADD COLUMN IF NOT EXISTS is the established
-- pattern for additive column amendments (migrations/00340:42-46
-- uses the same shape). On a second replay the column already
-- exists; IF NOT EXISTS turns the ALTER into a no-op.
--
-- Forward-only: the column is purely additive; rollback would
-- orphan any rows that have already been collapsed. Down is a
-- sentinel SELECT 1 so a replay lands on the ALTER, not on a
-- destructive DROP COLUMN.

ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS count int NOT NULL DEFAULT 1
    CHECK (count >= 1);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
