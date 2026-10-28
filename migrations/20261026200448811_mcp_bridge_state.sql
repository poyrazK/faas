-- filename: 20261026200448811_mcp_bridge_state.sql
-- migration-compat: expand
-- ADR-20261026195952: durable state of the hosted management MCP bridge.
-- Each mcp_bridge_state row is one namespace of a bridge's state (OAuth
-- clients and grants, each grant's plans), sealed with AES-256-GCM under a key
-- the database never sees. A bridge writes only in a transaction that still
-- holds its mcp_bridge_leases row at the same epoch, so an instance that lost
-- the lease cannot overwrite its successor's state.
--
-- The previous release neither reads nor writes these tables.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS mcp_bridge_leases (
    bridge     text        PRIMARY KEY CHECK (bridge ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    holder     text        NOT NULL CHECK (holder ~ '^[0-9a-f]{32}$'),
    epoch      bigint      NOT NULL CHECK (epoch > 0),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at))
);
CREATE TABLE IF NOT EXISTS mcp_bridge_state (
    bridge     text        NOT NULL REFERENCES mcp_bridge_leases (bridge) ON DELETE CASCADE,
    namespace  text        NOT NULL CHECK (octet_length(namespace) BETWEEN 1 AND 256),
    sealed     bytea       NOT NULL CHECK (octet_length(sealed) BETWEEN 29 AND 33554432),
    epoch      bigint      NOT NULL CHECK (epoch > 0),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
    PRIMARY KEY (bridge, namespace)
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS mcp_bridge_state;
DROP TABLE IF EXISTS mcp_bridge_leases;
