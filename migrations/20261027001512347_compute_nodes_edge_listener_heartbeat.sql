-- filename: 20261027001512347_compute_nodes_edge_listener_heartbeat.sql
-- migration-compat: expand
-- ADR-091 amendment (2026-10-27): each gatewayd-internal stamps its node's
-- edge_listener_heartbeat_at from its pg_notify loop, and apid's edge-rule
-- convergence barrier stops waiting for a gateway whose stamp is stale. NULL
-- means the node's gateway predates the column; apid still waits for it.
-- The previous release neither reads nor writes the column.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS edge_listener_heartbeat_at timestamptz;
COMMENT ON COLUMN compute_nodes.edge_listener_heartbeat_at IS
    'Last time this node''s gatewayd-internal edge-rule listener reported alive (database clock); NULL before its first report (ADR-091).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE compute_nodes DROP COLUMN IF EXISTS edge_listener_heartbeat_at;
