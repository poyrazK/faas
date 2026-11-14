-- name: InsertAgentUndoRecord :exec
-- ADR-20261112080601: written next to the agent's activity row.
INSERT INTO agent_undo_records (
    id, activity_id, key_id, account_id, org_id, method, path, kind, summary,
    inverse, guard, not_undoable_reason, created_at
) VALUES (
    sqlc.arg(id)::uuid, sqlc.arg(activity_id)::uuid, sqlc.arg(key_id)::uuid, sqlc.arg(account_id)::uuid,
    sqlc.narg(org_id)::uuid, sqlc.arg(method)::text, sqlc.arg(path)::text, sqlc.arg(kind)::text,
    sqlc.arg(summary)::text, sqlc.narg(inverse)::jsonb, sqlc.narg(guard)::jsonb,
    sqlc.narg(not_undoable_reason)::text, sqlc.arg(created_at)::timestamptz
);

-- name: ListAgentUndoRecords :many
-- A key's records since a time, newest first.
SELECT id, activity_id, key_id, account_id, org_id, method, path, kind, summary,
       inverse, guard, not_undoable_reason, created_at, undone_at, undone_by
FROM agent_undo_records
WHERE key_id = sqlc.arg(key_id)::uuid
  AND created_at >= sqlc.arg(since)::timestamptz
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: MarkAgentUndoRecordUndone :execrows
-- Only once: a record already undone is left as it is.
UPDATE agent_undo_records
SET undone_at = now(), undone_by = sqlc.arg(undone_by)::uuid
WHERE id = sqlc.arg(id)::uuid
  AND undone_at IS NULL
  AND inverse IS NOT NULL;

-- name: DeleteAgentUndoRecordsBefore :execrows
DELETE FROM agent_undo_records WHERE created_at < sqlc.arg(before)::timestamptz;
