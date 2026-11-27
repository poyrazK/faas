-- ADR-20261118154632: elastic builder slots.

-- name: BuildPreemptions :one
-- How many times borrowed slots gave this build back to the queue.
SELECT preemptions FROM builds WHERE id = sqlc.arg(build_id)::uuid;

-- name: RequeuePreemptedBuild :execrows
-- Returns a running claim to the queue after a borrowed slot gave its memory
-- back to tenants, and counts the give-back. Fenced like RequeueBuildIfClaim:
-- a stale worker cannot requeue a newer claim. enqueued_at is untouched, so
-- the build keeps its place in the queue.
UPDATE builds
   SET status = 'queued', started_at = NULL, preemptions = preemptions + 1
 WHERE id = sqlc.arg(claim_id)::uuid
   AND deployment_id = sqlc.arg(deployment_id)::uuid
   AND status = 'running'
   AND started_at = sqlc.arg(claim_started_at)::timestamptz;
