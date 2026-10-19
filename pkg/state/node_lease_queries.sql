-- ADR-1344 §4 (P3): compute-node liveness leases, on database time.

-- RenewNodeLease renews the caller's lease, or takes it over with the next
-- epoch once the previous holder's lease has expired. While another holder's
-- lease is live the conflict update is skipped and no row is returned.
-- name: RenewNodeLease :one
INSERT INTO node_leases (node_id, holder, epoch, renewed_at, expires_at)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(holder)::text, 1, clock_timestamp(),
        clock_timestamp() + make_interval(secs => sqlc.arg(ttl_seconds)::double precision))
ON CONFLICT (node_id) DO UPDATE SET
    epoch      = CASE WHEN node_leases.holder = EXCLUDED.holder THEN node_leases.epoch ELSE node_leases.epoch + 1 END,
    holder     = EXCLUDED.holder,
    renewed_at = EXCLUDED.renewed_at,
    expires_at = EXCLUDED.expires_at
WHERE node_leases.holder = EXCLUDED.holder OR node_leases.expires_at < clock_timestamp()
RETURNING node_id::text AS node_id, holder, epoch, renewed_at, expires_at, clock_timestamp()::timestamptz AS db_now;

-- name: GetNodeLease :one
SELECT node_id::text AS node_id, holder, epoch, renewed_at, expires_at, clock_timestamp()::timestamptz AS db_now
FROM node_leases
WHERE node_id = sqlc.arg(node_id)::uuid;
