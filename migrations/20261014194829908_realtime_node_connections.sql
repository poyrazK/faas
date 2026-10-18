-- migration-compat: expand
-- +goose Up
-- ADR-1323 fair use: realtimed reports each account's open connections on its
-- node every few seconds so admission can cap an account across the fleet. A
-- sync replaces the node's whole set; accounts without connections have no
-- row. Counts from a node whose last sync is stale are ignored, and nodes
-- silent for an hour are removed with their rows.
CREATE TABLE IF NOT EXISTS realtime_node_connection_syncs (
    node_name text        PRIMARY KEY CHECK (length(node_name) BETWEEN 1 AND 253),
    synced_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS realtime_node_connections (
    node_name   text    NOT NULL REFERENCES realtime_node_connection_syncs(node_name) ON DELETE CASCADE,
    account_id  uuid    NOT NULL,
    connections integer NOT NULL CHECK (connections > 0),
    PRIMARY KEY (node_name, account_id)
);
CREATE INDEX IF NOT EXISTS realtime_node_connections_account ON realtime_node_connections (account_id);

-- +goose Down
DROP TABLE IF EXISTS realtime_node_connections;
DROP TABLE IF EXISTS realtime_node_connection_syncs;
