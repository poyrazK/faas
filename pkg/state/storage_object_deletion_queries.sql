-- ADR-20261115180000: object deletion moves to the control plane.

-- name: RequestStorageObjectDeletion :exec
-- A fresh request, whatever the row held before; the trigger stamps a
-- per-node login's node. generation (slice 2) conditions the delete on one
-- object generation; NULL deletes whatever the key holds.
INSERT INTO storage_object_deletions (storage_key, reason, requested_by, generation)
VALUES (sqlc.arg(storage_key)::text, sqlc.arg(reason)::text, session_user, sqlc.narg(generation)::bigint)
ON CONFLICT (storage_key) DO UPDATE SET
    reason = EXCLUDED.reason, requested_by = session_user, requested_at = clock_timestamp(),
    generation = EXCLUDED.generation,
    next_attempt_at = clock_timestamp() + interval '10 minutes', state = 'requested',
    attempts = 0, last_error = '', decided_at = NULL;

-- name: DueStorageObjectDeletions :many
SELECT storage_key, reason, requested_by, requested_at, attempts, generation FROM storage_object_deletions
WHERE state = 'requested' AND next_attempt_at <= sqlc.arg(now)::timestamptz
ORDER BY next_attempt_at, storage_key
LIMIT sqlc.arg(max_rows)::int;

-- name: DecideStorageObjectDeletion :execrows
-- Records an outcome for the request that was checked; a newer request for
-- the key (a later requested_at) stays requested.
UPDATE storage_object_deletions SET state = sqlc.arg(state)::text, decided_at = clock_timestamp(),
    last_error = sqlc.arg(last_error)::text
WHERE storage_key = sqlc.arg(storage_key)::text AND requested_at = sqlc.arg(requested_at)::timestamptz
  AND state = 'requested';

-- name: RetryStorageObjectDeletion :execrows
UPDATE storage_object_deletions SET attempts = attempts + 1, last_error = sqlc.arg(last_error)::text,
    next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE storage_key = sqlc.arg(storage_key)::text AND requested_at = sqlc.arg(requested_at)::timestamptz
  AND state = 'requested';

-- name: SnapshotObjectReferenced :one
-- Whether a live row names the object: the exact key, the memory key of
-- its capture, or (for a v2 capture) any object of the capture directory.
SELECT EXISTS (SELECT 1 FROM snapshots s WHERE NOT s.delete_pending
        AND (s.storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text)
             OR (sqlc.arg(family)::text <> '' AND left(s.storage_key, length(sqlc.arg(family)::text)) = sqlc.arg(family)::text)))
    OR EXISTS (SELECT 1 FROM crash_captures c WHERE c.status <> 'expired'
        AND (c.storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text) OR c.vmstate_storage_key = sqlc.arg(storage_key)::text
             OR (sqlc.arg(family)::text <> '' AND left(c.storage_key, length(sqlc.arg(family)::text)) = sqlc.arg(family)::text)))
    OR EXISTS (SELECT 1 FROM environment_branch_captures b
        WHERE b.storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text)
           OR (sqlc.arg(family)::text <> '' AND left(b.storage_key, length(sqlc.arg(family)::text)) = sqlc.arg(family)::text))
    OR EXISTS (SELECT 1 FROM environment_branch_capture_objects o
        WHERE o.storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text)
           OR (sqlc.arg(family)::text <> '' AND left(o.storage_key, length(sqlc.arg(family)::text)) = sqlc.arg(family)::text))
    OR EXISTS (SELECT 1 FROM application_standard_snapshot_captures a
        WHERE a.memory_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text)
           OR (sqlc.arg(family)::text <> '' AND left(a.memory_key, length(sqlc.arg(family)::text)) = sqlc.arg(family)::text))
    OR EXISTS (SELECT 1 FROM runtime_snapshots r WHERE r.state = 'ready'
        AND (r.storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text)
             OR (sqlc.arg(family)::text <> '' AND left(r.storage_key, length(sqlc.arg(family)::text)) = sqlc.arg(family)::text)))
    OR EXISTS (SELECT 1 FROM migration_leases m
        WHERE m.mem_storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text) OR m.vmstate_storage_key = sqlc.arg(storage_key)::text)
    OR EXISTS (SELECT 1 FROM migration_admissions m WHERE m.expires_at > clock_timestamp()
        AND (m.mem_storage_key IN (sqlc.arg(storage_key)::text, sqlc.arg(mem_key)::text) OR m.vmstate_storage_key = sqlc.arg(storage_key)::text))
    AS referenced;

-- name: JobArtifactReferenced :one
-- ADR-20261117190000: a jobs/ key is live while its job is not deleted and
-- either still materializing (a worker may be writing an unpublished
-- attempt) or publishes exactly this key.
SELECT EXISTS (
    SELECT 1 FROM jobs j
     WHERE j.id = sqlc.arg(job_id)::uuid AND j.status <> 'deleted'
       AND (j.image_materialization_status NOT IN ('ready', 'failed')
            OR j.image_storage_key = sqlc.arg(storage_key)::text)
)::boolean AS referenced;

-- name: BuildSourceReferenced :one
-- A build's source archive is live while the build is queued or running.
SELECT EXISTS (
    SELECT 1 FROM builds WHERE id = sqlc.arg(id)::uuid AND status IN ('queued', 'running')
)::boolean AS referenced;
