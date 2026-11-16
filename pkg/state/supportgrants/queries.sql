-- Customer-granted support access (ADR-20261116163508).

-- name: InsertGrant :one
INSERT INTO support_grants (org_id, app_ids, created_by_account_id, request_id, reason, expires_at)
VALUES (sqlc.arg(org_id)::uuid, sqlc.narg(app_ids)::uuid[], sqlc.arg(created_by)::uuid, sqlc.narg(request_id)::uuid,
        sqlc.arg(reason)::text, clock_timestamp() + make_interval(secs => sqlc.arg(ttl_seconds)::double precision))
RETURNING id, org_id, app_ids, created_by_account_id, request_id, reason, created_at, expires_at, revoked_at, revoked_by_account_id;

-- name: GrantByID :one
SELECT g.id, g.org_id, g.app_ids, g.created_by_account_id, g.request_id, g.reason, g.created_at, g.expires_at,
       g.revoked_at, g.revoked_by_account_id, o.slug AS org_slug
FROM support_grants g JOIN orgs o ON o.id = g.org_id
WHERE g.id = sqlc.arg(id)::uuid;

-- A grant is usable while it is unrevoked and unexpired and its creator is
-- still an active owner or admin of the organization (or the owner of a
-- personal organization). Losing that role ends the grant.
-- name: UsableGrant :one
SELECT g.id, g.org_id, g.app_ids, g.expires_at
FROM support_grants g
JOIN orgs o ON o.id = g.org_id
JOIN accounts a ON a.id = g.created_by_account_id AND a.status IN ('active', 'past_due')
WHERE g.id = sqlc.arg(id)::uuid AND g.revoked_at IS NULL AND g.expires_at > now()
  AND (EXISTS (SELECT 1 FROM org_memberships m
               WHERE m.org_id = g.org_id AND m.account_id = g.created_by_account_id
                 AND m.removed_at IS NULL AND m.role IN ('owner', 'admin'))
       OR (o.personal_org AND o.personal_owner_account_id = g.created_by_account_id));

-- Every live grant of the organization, then its most recent ended ones, so
-- a long-lived grant never drops out of view behind newer history.
-- name: ListGrants :many
(SELECT g.id, g.org_id, g.app_ids, g.created_by_account_id, g.request_id, g.reason, g.created_at, g.expires_at,
        g.revoked_at, g.revoked_by_account_id, o.slug AS org_slug
 FROM support_grants g JOIN orgs o ON o.id = g.org_id
 WHERE g.org_id = sqlc.arg(org_id)::uuid AND g.revoked_at IS NULL AND g.expires_at > now())
UNION ALL
(SELECT g.id, g.org_id, g.app_ids, g.created_by_account_id, g.request_id, g.reason, g.created_at, g.expires_at,
        g.revoked_at, g.revoked_by_account_id, o.slug AS org_slug
 FROM support_grants g JOIN orgs o ON o.id = g.org_id
 WHERE g.org_id = sqlc.arg(org_id)::uuid AND (g.revoked_at IS NOT NULL OR g.expires_at <= now())
 ORDER BY g.created_at DESC
 LIMIT sqlc.arg(history_limit)::int);

-- name: ListLiveGrants :many
SELECT g.id, g.org_id, g.app_ids, g.created_by_account_id, g.request_id, g.reason, g.created_at, g.expires_at,
       g.revoked_at, g.revoked_by_account_id, o.slug AS org_slug
FROM support_grants g JOIN orgs o ON o.id = g.org_id
WHERE g.revoked_at IS NULL AND g.expires_at > now()
ORDER BY g.expires_at
LIMIT 200;

-- name: RevokeGrant :execrows
UPDATE support_grants
SET revoked_at = clock_timestamp(), revoked_by_account_id = sqlc.arg(revoked_by)::uuid
WHERE id = sqlc.arg(id)::uuid AND org_id = sqlc.arg(org_id)::uuid
  AND revoked_at IS NULL AND expires_at > now();

-- LockOrgRequests serializes request creation per organization, so the open
-- request cap holds under concurrent requests.
-- name: LockOrgRequests :exec
SELECT pg_advisory_xact_lock(hashtextextended('support_grant_requests:' || sqlc.arg(org_id)::uuid::text, 0));

-- At most max_open open, unexpired requests per organization; run under
-- LockOrgRequests in the same transaction.
-- name: InsertRequest :one
INSERT INTO support_grant_requests (org_id, operator_account_id, reason, expires_at)
SELECT sqlc.arg(org_id)::uuid, sqlc.arg(operator_id)::uuid, sqlc.arg(reason)::text,
       clock_timestamp() + make_interval(secs => sqlc.arg(ttl_seconds)::double precision)
WHERE (SELECT count(*) FROM support_grant_requests r
       WHERE r.org_id = sqlc.arg(org_id)::uuid AND r.status = 'open' AND r.expires_at > now()) < sqlc.arg(max_open)::int
RETURNING id, org_id, operator_account_id, reason, status, created_at, expires_at, decided_at, decided_by_account_id;

-- name: ListRequests :many
SELECT id, org_id, operator_account_id, reason, status, created_at, expires_at, decided_at, decided_by_account_id
FROM support_grant_requests
WHERE org_id = sqlc.arg(org_id)::uuid
ORDER BY created_at DESC
LIMIT 50;

-- name: DecideRequest :one
UPDATE support_grant_requests
SET status = sqlc.arg(status)::text, decided_at = clock_timestamp(), decided_by_account_id = sqlc.arg(decided_by)::uuid
WHERE id = sqlc.arg(id)::uuid AND org_id = sqlc.arg(org_id)::uuid AND status = 'open' AND expires_at > now()
RETURNING id, org_id, operator_account_id, reason, status, created_at, expires_at, decided_at, decided_by_account_id;
