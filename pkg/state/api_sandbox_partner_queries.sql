-- name: CreateAPISandboxPartner :exec
INSERT INTO api_sandbox_partners (app_id, label) VALUES ($1, $2);

-- name: GetAPISandboxPartner :one
SELECT * FROM api_sandbox_partners WHERE app_id = $1;

-- name: LockAPISandboxPartner :one
SELECT p.* FROM api_sandbox_partners p JOIN apps a ON a.id = p.app_id
WHERE p.app_id = sqlc.arg(app_id)
  AND a.status = 'active' AND a.preview_pr_state = 'open' AND a.preview_expires_at > sqlc.arg(at_time)
FOR UPDATE OF p;

-- name: SetAPISandboxPartnerInvite :one
UPDATE api_sandbox_partners p
SET invite_hash = sqlc.arg(invite_hash), invited_at = sqlc.arg(at_time), redeemed_at = NULL,
    revoked_at = NULL, consumer_key_id = NULL, first_success_at = NULL
FROM apps a
WHERE p.app_id = sqlc.arg(app_id) AND a.id = p.app_id
  AND a.status = 'active' AND a.preview_pr_state = 'open' AND a.preview_expires_at > sqlc.arg(at_time)
  AND (p.redeemed_at IS NULL OR p.revoked_at IS NOT NULL)
RETURNING p.*;

-- name: RedeemAPISandboxPartnerInvite :exec
UPDATE api_sandbox_partners
SET redeemed_at = sqlc.arg(at_time), consumer_key_id = sqlc.arg(consumer_key_id), invite_hash = NULL
WHERE app_id = sqlc.arg(app_id);

-- name: RevokeAPISandboxPartner :one
UPDATE api_sandbox_partners
SET invite_hash = NULL, revoked_at = coalesce(revoked_at, sqlc.arg(at_time))
WHERE app_id = sqlc.arg(app_id)
RETURNING *;

-- name: RevokeAPISandboxPartnerKey :exec
UPDATE consumer_keys
SET revoked_at = coalesce(revoked_at, sqlc.arg(at_time))
WHERE id = sqlc.arg(key_id) AND app_id = sqlc.arg(app_id);

-- name: APISandboxPartnerKeyActive :one
SELECT EXISTS(
    SELECT 1 FROM api_sandbox_partners
    WHERE app_id = sqlc.arg(app_id) AND consumer_key_id = sqlc.arg(key_id) AND revoked_at IS NULL
);

-- name: RecordAPISandboxPartnerSuccess :exec
UPDATE api_sandbox_partners
SET first_success_at = greatest(redeemed_at, sqlc.arg(succeeded_at))
WHERE app_id = sqlc.arg(app_id) AND consumer_key_id = sqlc.arg(key_id)
  AND revoked_at IS NULL
  AND (first_success_at IS NULL OR first_success_at > greatest(redeemed_at, sqlc.arg(succeeded_at)));
