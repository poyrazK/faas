-- +goose Up
-- +goose StatementBegin
-- Shared short-lived reservations let independent schedd processes account
-- for snapshot restore I/O pressure on the same compute node. Expiry recovers
-- reservations if a schedd exits before its vmmd RPC returns.
CREATE TABLE IF NOT EXISTS snapshot_restore_pressure_leases (
    lease_id   uuid PRIMARY KEY,
    node_id    uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS snapshot_restore_pressure_leases_node_expiry_idx
    ON snapshot_restore_pressure_leases (node_id, expires_at);
CREATE INDEX IF NOT EXISTS snapshot_restore_pressure_leases_expiry_idx
    ON snapshot_restore_pressure_leases (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE snapshot_restore_pressure_leases;
-- +goose StatementEnd
