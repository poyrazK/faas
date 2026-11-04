-- filename: 20261102175613200_request_telemetry_route_evidence_flags.sql

-- ADR-20261028110044 amendment 5: deployment_pinned marks a request that named
-- its deployment (a deployment preview or alias URL, or a revision pin), which
-- route evidence leaves out because it is not production traffic. long_lived
-- marks a response that stayed open past its request budget (a WebSocket or a
-- declared stream), whose duration route evidence leaves out of latency.
-- Constant defaults keep both changes metadata-only on every partition.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE request_telemetry ADD COLUMN IF NOT EXISTS deployment_pinned boolean NOT NULL DEFAULT false;
ALTER TABLE request_telemetry ADD COLUMN IF NOT EXISTS long_lived boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE request_telemetry DROP COLUMN IF EXISTS long_lived;
ALTER TABLE request_telemetry DROP COLUMN IF EXISTS deployment_pinned;
