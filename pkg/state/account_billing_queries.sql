-- Only supplied fields are written; concurrent disjoint PATCHes commute.
-- A non-null empty string explicitly clears a field.
-- name: PatchPersonalAccountBillingInfo :execrows
UPDATE accounts SET
 business_name=CASE WHEN sqlc.narg(business_name)::text IS NULL THEN business_name ELSE nullif(sqlc.narg(business_name)::text,'') END,
 billing_address=CASE WHEN sqlc.narg(billing_address)::text IS NULL THEN billing_address ELSE nullif(sqlc.narg(billing_address)::text,'') END,
 tax_id=CASE WHEN sqlc.narg(tax_id)::text IS NULL THEN tax_id ELSE nullif(sqlc.narg(tax_id)::text,'') END
WHERE id=sqlc.arg(id)::uuid;

-- name: PatchOrgBillingInfo :execrows
UPDATE orgs SET
 business_name=coalesce(sqlc.narg(business_name)::text,business_name),
 billing_address=coalesce(sqlc.narg(billing_address)::text,billing_address),
 tax_id=coalesce(sqlc.narg(tax_id)::text,tax_id),updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND NOT personal_org;

-- The organization is the financial subject after resource adoption; its
-- identity writes must serialize on that row rather than a human account.
-- name: LockBillingIdentityOrg :one
SELECT id FROM orgs WHERE id=$1 AND NOT personal_org FOR NO KEY UPDATE;

-- Called with the financial subject's row locked, as identity writers do.
-- name: BillingIdentityForMutation :one
SELECT customer_id,subscription_id FROM billing_identities WHERE account_id=$1 AND provider=$2;
