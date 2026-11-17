-- A member without two-factor authentication is not admitted to an
-- organization that requires it (ADR-20261114170352).
-- name: Snapshot :one
SELECT m.role, m.access_revision FROM org_memberships m
WHERE m.org_id = $1 AND m.account_id = $2 AND m.removed_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM org_security_policies p JOIN accounts a ON a.id = m.account_id
    WHERE p.org_id = m.org_id AND p.require_mfa AND a.mfa_enrolled_at IS NULL);

-- name: LockMembership :one
SELECT m.role, m.access_revision FROM org_memberships m
WHERE m.org_id = $1 AND m.account_id = $2 AND m.removed_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM org_security_policies p JOIN accounts a ON a.id = m.account_id
    WHERE p.org_id = m.org_id AND p.require_mfa AND a.mfa_enrolled_at IS NULL)
FOR SHARE OF m;

-- name: LockAPIKey :one
SELECT id FROM api_keys
WHERE id = $1 AND account_id = $2 AND org_id = $3
  AND status IN ('active', 'grace') AND (expires_at IS NULL OR expires_at > now())
FOR SHARE;

-- A token authenticates as its minter when it has one (ADR 20261110190500).
-- name: LockDeployToken :one
SELECT id FROM deploy_tokens
WHERE id = sqlc.arg(id) AND coalesce(created_by_account_id, account_id) = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)
  AND status IN ('active', 'grace') AND expires_at > now()
FOR SHARE;

-- name: LockOIDCBearer :one
SELECT id FROM oidc_exchanged_tokens
WHERE id = $1 AND account_id = $2 AND expires_at > now()
FOR SHARE;

-- A support grant fences a support request instead of a membership
-- (ADR-20261116163508): the grant must still be unrevoked, unexpired and
-- held by an active owner or admin. Revoking takes a row lock, so a revoke
-- and an in-flight support write have a database-defined order.
-- name: LockSupportGrant :one
SELECT g.id FROM support_grants g
JOIN orgs o ON o.id = g.org_id
JOIN accounts a ON a.id = g.created_by_account_id AND a.status IN ('active', 'past_due')
WHERE g.id = sqlc.arg(id)::uuid AND g.org_id = sqlc.arg(org_id)::uuid
  AND g.revoked_at IS NULL AND g.expires_at > now()
  AND (EXISTS (SELECT 1 FROM org_memberships m
               WHERE m.org_id = g.org_id AND m.account_id = g.created_by_account_id
                 AND m.removed_at IS NULL AND m.role IN ('owner', 'admin'))
       OR (o.personal_org AND o.personal_owner_account_id = g.created_by_account_id))
FOR SHARE OF g;

-- CheckSupportGrant is LockSupportGrant for a read-only transaction, where
-- PostgreSQL prohibits row locks.
-- name: CheckSupportGrant :one
SELECT g.id FROM support_grants g
JOIN orgs o ON o.id = g.org_id
JOIN accounts a ON a.id = g.created_by_account_id AND a.status IN ('active', 'past_due')
WHERE g.id = sqlc.arg(id)::uuid AND g.org_id = sqlc.arg(org_id)::uuid
  AND g.revoked_at IS NULL AND g.expires_at > now()
  AND (EXISTS (SELECT 1 FROM org_memberships m
               WHERE m.org_id = g.org_id AND m.account_id = g.created_by_account_id
                 AND m.removed_at IS NULL AND m.role IN ('owner', 'admin'))
       OR (o.personal_org AND o.personal_owner_account_id = g.created_by_account_id));

-- name: NotifyIntent :exec
SELECT pg_notify(sqlc.arg(channel)::text, sqlc.arg(payload)::text);
