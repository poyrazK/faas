-- Prod hunt r72: live snapshots a node's fan-out found missing from the
-- canonical store, where the snapshot was already older than the grace when
-- the failure was recorded.

-- name: SnapshotsMissingFromOrigin :many
SELECT sn.id::text AS id
  FROM snapshots sn
 WHERE NOT sn.stale
   AND sn.storage_key <> ''
   AND sn.created_at < now() - make_interval(secs => sqlc.arg(grace_seconds)::float8)
   AND EXISTS (
       SELECT 1 FROM snapshot_replicas r
        WHERE r.snapshot_id = sn.id
          AND r.state = 'failed'
          AND r.next_attempt_at IS NULL
          AND r.last_error LIKE sqlc.arg(error_pattern)::text
          AND r.updated_at >= sn.created_at + make_interval(secs => sqlc.arg(grace_seconds)::float8))
 ORDER BY sn.created_at
 LIMIT sqlc.arg(row_limit)::int;
