-- Projection of migration 20261026200448811 (ADR-20261026195952).
CREATE TABLE mcp_bridge_leases (
    bridge     text        PRIMARY KEY CHECK (bridge ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    holder     text        NOT NULL CHECK (holder ~ '^[0-9a-f]{32}$'),
    epoch      bigint      NOT NULL CHECK (epoch > 0),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at))
);
CREATE TABLE mcp_bridge_state (
    bridge     text        NOT NULL REFERENCES mcp_bridge_leases (bridge) ON DELETE CASCADE,
    namespace  text        NOT NULL CHECK (octet_length(namespace) BETWEEN 1 AND 256),
    sealed     bytea       NOT NULL CHECK (octet_length(sealed) BETWEEN 29 AND 33554432),
    epoch      bigint      NOT NULL CHECK (epoch > 0),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
    PRIMARY KEY (bridge, namespace)
);
