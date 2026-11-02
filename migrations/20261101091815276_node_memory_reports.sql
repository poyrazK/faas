-- filename: 20261101091815276_node_memory_reports.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks compute_nodes briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR 20261101091815: each compute node's own scheduler measures its node's
-- memory (ADR-909) and publishes it here every few seconds, so a scheduler
-- placing onto a peer node admits by that node's measured memory instead of
-- its plan-RAM bookings at 100%, and handles the node's memory pressure.
-- measured_mb is the ADR-909 measured charge of every instance on the node,
-- used_mb vmmd's summed cgroup memory.current (NULL without a fresh vmmd
-- report), frozen_instances the node's frozen VMs, measured_at when the
-- measurement was taken. A new table, so older schedulers keep working.
CREATE TABLE IF NOT EXISTS node_memory_reports (
    node_id          uuid PRIMARY KEY REFERENCES compute_nodes(id) ON DELETE CASCADE,
    measured_mb      integer NOT NULL CHECK (measured_mb >= 0),
    used_mb          integer CHECK (used_mb >= 0),
    frozen_instances integer NOT NULL CHECK (frozen_instances >= 0),
    measured_at      timestamptz NOT NULL,
    reported_at      timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE node_memory_reports IS
    'ADR 20261101091815 node-wide measured memory, published by each node''s own scheduler for its peers.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS node_memory_reports;
-- +goose StatementEnd
