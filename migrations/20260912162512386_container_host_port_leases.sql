-- +goose Up
-- +goose StatementBegin

-- Durable node-local mappings for declared container listeners. The lease is
-- separate from the vmmd bridge address: the default ingress path continues
-- to use the per-instance host IP, while direct host binding can consume this
-- registry without racing another instance on the same compute node.
CREATE TABLE IF NOT EXISTS container_host_port_leases (
    node_id       uuid        NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    instance_id   uuid        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    listener_name text        NOT NULL CHECK (listener_name ~ '^[a-z0-9][a-z0-9-]{0,30}$'),
    protocol      text        NOT NULL CHECK (protocol IN ('tcp', 'udp')),
    guest_port    integer     NOT NULL CHECK (guest_port BETWEEN 1 AND 65535),
    host_port     integer     NOT NULL CHECK (host_port BETWEEN 30000 AND 39999),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, instance_id, listener_name, protocol),
    UNIQUE (node_id, protocol, host_port)
);

CREATE INDEX IF NOT EXISTS container_host_port_leases_instance_idx
    ON container_host_port_leases (instance_id);

-- A node-local host-port row is only useful while its instance is resident.
-- The scheduler releases rows on PARKED/STOPPED/FAILED transitions; this
-- index keeps the restart reconciliation query cheap for the recovery loop.
CREATE INDEX IF NOT EXISTS container_host_port_leases_node_idx
    ON container_host_port_leases (node_id, protocol, host_port);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS container_host_port_leases_node_idx;
DROP INDEX IF EXISTS container_host_port_leases_instance_idx;
DROP TABLE IF EXISTS container_host_port_leases;
-- +goose StatementEnd
