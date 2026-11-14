-- filename: 20261109144434520_realtime_node_endpoint_connections.sql
-- migration-compat: expand
-- An endpoint's max_connections caps the endpoint across the fleet, as
-- docs/realtime.md says, not once per realtime node. realtimed reports each
-- capped endpoint's open connections on its node with the ADR-1323 account
-- sync, and learns the other nodes' counts back. A sync replaces the node's
-- whole set; endpoints without connections have no row. The rows go with
-- their node (ON DELETE CASCADE) when it stops syncing.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS realtime_node_endpoint_connections (
    node_name   text    NOT NULL REFERENCES realtime_node_connection_syncs(node_name) ON DELETE CASCADE,
    endpoint_id uuid    NOT NULL,
    connections integer NOT NULL CHECK (connections > 0),
    PRIMARY KEY (node_name, endpoint_id)
);
CREATE INDEX IF NOT EXISTS realtime_node_endpoint_connections_endpoint ON realtime_node_endpoint_connections (endpoint_id);

-- +goose Down
DROP TABLE IF EXISTS realtime_node_endpoint_connections;
