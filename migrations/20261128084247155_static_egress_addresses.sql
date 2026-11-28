-- filename: 20261128084247155_static_egress_addresses.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- Operator inventory of additional public IPv4 addresses for static egress
-- (ADR-20261128090000). Each address is routed by its provider to exactly one
-- compute node; schedd places a pinned app on that node and vmmd on that node
-- aliases it. account_id is the tenant the operator assigned the address to;
-- NULL is an unassigned address. The legacy per-node TOML bundle grants
-- (static_egress_provisioning_grants) remain readable beside it.
CREATE TABLE IF NOT EXISTS static_egress_addresses (
    ip INET PRIMARY KEY CHECK (family(ip) = 4 AND masklen(ip) = 32),
    node_id UUID NOT NULL REFERENCES compute_nodes(id),
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS static_egress_addresses_node_idx
    ON static_egress_addresses (node_id);
CREATE INDEX IF NOT EXISTS static_egress_addresses_account_idx
    ON static_egress_addresses (account_id) WHERE account_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS static_egress_addresses;
-- +goose StatementEnd
