-- filename: 20261019173309027_request_telemetry_trace_inferred.sql

-- ADR-1138: a service call that Gregale linked to its caller's trace (the
-- caller sent no traceparent and was serving exactly one request) is marked
-- so consumers can tell an inferred link from propagated trace context. A
-- constant default keeps the change metadata-only on every partition.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE request_telemetry ADD COLUMN IF NOT EXISTS trace_inferred boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE request_telemetry DROP COLUMN IF EXISTS trace_inferred;
