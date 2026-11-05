-- ADR-20261102100000: build coalescing.

-- name: RunningIdenticalBuild :one
-- The earliest-claimed other build that is building the same cache key now:
-- it missed the cache, is running, is not stale, and was claimed before the
-- asking build, so two identical builds never wait for each other.
SELECT id FROM builds
WHERE status = 'running'
  AND cache_status = 'miss'
  AND cache_key_sha256 = sqlc.arg(cache_key_sha256)::text
  AND id <> sqlc.arg(claim_id)::uuid
  AND started_at > now() - interval '30 minutes'
  AND (started_at < sqlc.arg(claim_started_at)::timestamptz
       OR (started_at = sqlc.arg(claim_started_at)::timestamptz AND id < sqlc.arg(claim_id)::uuid))
ORDER BY started_at, id
LIMIT 1;

-- name: DeferBuildBehind :execrows
-- Requeues a running claim to wait for an identical build. Fenced like
-- RequeueBuildIfClaim: a stale worker cannot defer a newer claim.
UPDATE builds
   SET status = 'queued', started_at = NULL, coalesced_with = sqlc.arg(producer_id)::uuid
 WHERE id = sqlc.arg(claim_id)::uuid
   AND deployment_id = sqlc.arg(deployment_id)::uuid
   AND status = 'running'
   AND started_at = sqlc.arg(claim_started_at)::timestamptz
   AND sqlc.arg(producer_id)::uuid <> sqlc.arg(claim_id)::uuid;
