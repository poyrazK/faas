-- name: InsertAPIKeyApprovalCategories :exec
-- ADR-20261104140058: the categories a key must have approved; set once, when it is minted.
INSERT INTO api_key_approval_categories (key_id, categories)
VALUES (sqlc.arg(key_id)::uuid, sqlc.arg(categories)::text[]);

-- name: GetAPIKeyApprovalCategories :one
SELECT categories FROM api_key_approval_categories WHERE key_id = sqlc.arg(key_id)::uuid;

-- name: FindPendingAgentApproval :one
-- An undecided, unexpired request with the same binding, so a retried
-- request reuses its review link.
SELECT id, expires_at FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid
  AND method = sqlc.arg(method)
  AND path = sqlc.arg(path)
  AND body_sha256 IS NOT DISTINCT FROM sqlc.narg(body_sha256)::bytea
  AND status = 'pending'
  AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- name: CountPendingAgentApprovals :one
SELECT count(*)::int FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid AND status = 'pending' AND expires_at > now();

-- name: PruneAgentApprovals :exec
-- Requests of the key that expired a week ago or more.
DELETE FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid AND expires_at < now() - interval '7 days';

-- name: InsertAgentApproval :exec
INSERT INTO agent_approval_requests (id, key_id, account_id, org_id, category, method, path, body_sha256, expires_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(key_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(org_id)::uuid,
        sqlc.arg(category), sqlc.arg(method), sqlc.arg(path), sqlc.narg(body_sha256)::bytea, sqlc.arg(expires_at));

-- name: ConsumeAgentApproval :one
-- Uses one approved, unexpired request with the same binding, in the same
-- statement that finds it, so one approval allows exactly one request.
UPDATE agent_approval_requests
SET status = 'consumed', consumed_at = now()
WHERE id = (
    SELECT a.id FROM agent_approval_requests a
    WHERE a.key_id = sqlc.arg(key_id)::uuid
      AND a.method = sqlc.arg(method)
      AND a.path = sqlc.arg(path)
      AND a.body_sha256 IS NOT DISTINCT FROM sqlc.narg(body_sha256)::bytea
      AND a.status = 'approved'
      AND a.expires_at > now()
    ORDER BY a.decided_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id;

-- name: GetAgentApproval :one
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at
FROM agent_approval_requests WHERE id = sqlc.arg(id)::uuid;

-- name: DecideAgentApproval :execrows
-- A person approves or denies a request that is still pending and unexpired.
UPDATE agent_approval_requests
SET status = sqlc.arg(status), decided_by = sqlc.arg(decided_by)::uuid, decided_at = now()
WHERE id = sqlc.arg(id)::uuid AND status = 'pending' AND expires_at > now();

-- name: ListAgentApprovalsForKey :many
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at
FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: ListPendingAgentApprovals :many
-- Undecided requests of agents the account connected, or of agents acting in
-- the organizations given (those the account owns or administers).
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at
FROM agent_approval_requests
WHERE status = 'pending' AND expires_at > now()
  AND (account_id = sqlc.arg(account_id)::uuid OR org_id = ANY(sqlc.arg(org_ids)::uuid[]))
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows)::int;
