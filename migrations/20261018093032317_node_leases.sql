-- filename: 20261018093032317_node_leases.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-1344 §4 (P3, phase 1): one liveness lease per compute node, renewed by
-- the node's own schedd. Expiry is on database time so every reader agrees.
-- The epoch increases whenever a new holder takes the lease over, so later
-- phases can fence work by epoch. Phase 1 only records the lease; nothing
-- acts on it, and a node without a row is "unknown", never "expired".
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS node_leases (
    node_id    uuid PRIMARY KEY REFERENCES compute_nodes(id) ON DELETE CASCADE,
    holder     text NOT NULL CHECK (length(holder) BETWEEN 1 AND 255),
    epoch      bigint NOT NULL CHECK (epoch >= 1),
    renewed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > renewed_at)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS node_leases;
-- +goose StatementEnd
