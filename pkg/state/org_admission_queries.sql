-- Account lock serializes Free organization creation across apid processes.
-- name: LockSharedOrgCreator :one
SELECT id FROM accounts WHERE id=$1 FOR UPDATE;

-- A Free organization deleted this UTC month still holds its slot until the
-- month ends (ADR-20261104211412): its Free allowance was already used for the
-- month, so deleting and recreating it must not grant a fresh one.
-- name: CountOwnedFreeSharedOrgs :one
SELECT count(*) FROM orgs o JOIN org_memberships m ON m.org_id=o.id
WHERE m.account_id=$1 AND m.role='owner' AND m.removed_at IS NULL
  AND NOT o.personal_org AND o.plan='free'
  AND (NOT o.deleted_pending OR o.deletion_requested_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC');

-- name: InsertFreeSharedOrg :one
INSERT INTO orgs (id,slug,name) VALUES ($1,$2,$3)
RETURNING id,slug,name,plan,status,created_at,updated_at,resource_ownership_enabled;

-- name: InsertSharedOrgOwner :exec
INSERT INTO org_memberships (org_id,account_id,role,invited_by_account_id)
VALUES ($1,$2,'owner',$2);

-- Membership accepts and lifecycle/plan writes serialize on the organization.
-- NO KEY UPDATE remains compatible with foreign-key key-share locks.
-- name: LockOrgInvitationTarget :one
SELECT plan,status,deleted_pending,abuse_hold_at FROM orgs WHERE id=$1 FOR NO KEY UPDATE;

-- name: CountActiveOrgMembers :one
SELECT count(*) FROM org_memberships WHERE org_id=$1 AND removed_at IS NULL;

-- Pin the incoming plan while deciding whether a transfer consumes a Free slot.
-- name: LockOrgOwnershipTransferTarget :one
SELECT plan,personal_org,deleted_pending FROM orgs WHERE id=$1 FOR NO KEY UPDATE;
