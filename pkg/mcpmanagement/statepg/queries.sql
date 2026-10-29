-- Hosted management MCP bridge state (ADR-20261026195952).

-- name: AcquireMCPBridgeLease :one
-- Takes a free or expired lease with the next epoch. No row: another holder
-- still owns it.
INSERT INTO mcp_bridge_leases (bridge, holder, epoch, expires_at)
VALUES (@bridge, @holder, 1, now() + make_interval(secs => @ttl_seconds::double precision))
ON CONFLICT (bridge) DO UPDATE
SET holder = EXCLUDED.holder,
    epoch = mcp_bridge_leases.epoch + 1,
    expires_at = EXCLUDED.expires_at
WHERE mcp_bridge_leases.expires_at <= now()
RETURNING epoch;

-- name: RenewMCPBridgeLease :one
UPDATE mcp_bridge_leases
SET expires_at = now() + make_interval(secs => @ttl_seconds::double precision)
WHERE bridge = @bridge AND holder = @holder AND epoch = @epoch AND expires_at > now()
RETURNING expires_at;

-- name: ReleaseMCPBridgeLease :exec
UPDATE mcp_bridge_leases
SET expires_at = now()
WHERE bridge = @bridge AND holder = @holder AND epoch = @epoch;

-- name: HoldMCPBridgeLease :one
-- Locks the lease for the rest of the transaction while this holder still
-- owns it. No row: the lease was lost and the write must not commit.
SELECT epoch FROM mcp_bridge_leases
WHERE bridge = @bridge AND holder = @holder AND epoch = @epoch AND expires_at > now()
FOR UPDATE;

-- name: ListMCPBridgeState :many
SELECT namespace, sealed FROM mcp_bridge_state
WHERE bridge = @bridge
ORDER BY namespace;

-- name: PutMCPBridgeState :exec
INSERT INTO mcp_bridge_state (bridge, namespace, sealed, epoch, updated_at)
VALUES (@bridge, @namespace, @sealed, @epoch, now())
ON CONFLICT (bridge, namespace) DO UPDATE
SET sealed = EXCLUDED.sealed, epoch = EXCLUDED.epoch, updated_at = now();

-- name: DeleteMCPBridgeState :exec
DELETE FROM mcp_bridge_state
WHERE bridge = @bridge AND namespace = @namespace;

-- name: DeleteAllMCPBridgeState :exec
-- Drops state that no longer opens with the bridge's key.
DELETE FROM mcp_bridge_state
WHERE bridge = @bridge;
