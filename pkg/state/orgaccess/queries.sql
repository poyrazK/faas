-- name: Snapshot :one
SELECT role, access_revision FROM org_memberships
WHERE org_id = $1 AND account_id = $2 AND removed_at IS NULL;

-- name: LockMembership :one
SELECT role, access_revision FROM org_memberships
WHERE org_id = $1 AND account_id = $2 AND removed_at IS NULL
FOR SHARE;

-- name: LockAPIKey :one
SELECT id FROM api_keys
WHERE id = $1 AND account_id = $2 AND org_id = $3
  AND status IN ('active', 'grace') AND (expires_at IS NULL OR expires_at > now())
FOR SHARE;

-- name: LockDeployToken :one
SELECT id FROM deploy_tokens
WHERE id = $1 AND account_id = $2 AND app_id = $3
  AND status IN ('active', 'grace') AND expires_at > now()
FOR SHARE;

-- name: LockOIDCBearer :one
SELECT id FROM oidc_exchanged_tokens
WHERE id = $1 AND account_id = $2 AND expires_at > now()
FOR SHARE;

-- name: NotifyIntent :exec
SELECT pg_notify(sqlc.arg(channel)::text, sqlc.arg(payload)::text);
