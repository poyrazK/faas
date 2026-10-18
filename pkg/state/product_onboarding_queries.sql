-- name: ProductOnboardingOwner :one
SELECT c.plan::text FROM accounts c JOIN apps a ON a.account_id=c.id
WHERE c.id=$1 AND a.id=$2 AND a.status<>'deleted'
AND c.status IN ('active','past_due') AND c.abuse_hold_at IS NULL;

-- name: ProductOnboardingAuthApp :one
SELECT id FROM apps WHERE account_id=$1 AND id=$2 AND slug=$3 AND status<>'deleted';

-- name: ProductOnboardingTenant :one
SELECT id,account_id,external_ref,name,status,created_at,updated_at FROM platform_tenants
WHERE account_id=sqlc.arg(account_id) AND
 ((sqlc.narg(tenant_id)::uuid IS NOT NULL AND id=sqlc.narg(tenant_id)) OR
 (sqlc.narg(tenant_id)::uuid IS NULL AND external_ref=sqlc.arg(external_ref))) FOR UPDATE;

-- name: ProductOnboardingReadTenant :one
SELECT id,account_id,external_ref,name,status,created_at,updated_at FROM platform_tenants
WHERE account_id=$1 AND id=$2;

-- name: ProductOnboardingTenantCount :one
SELECT count(*)::bigint FROM platform_tenants WHERE account_id=$1;

-- name: ProductOnboardingCreateTenant :exec
INSERT INTO platform_tenants(id,account_id,external_ref,name,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$5);

-- name: ProductOnboardingInsert :exec
INSERT INTO product_control_onboardings(id,account_id,app_id,tenant_id,auth_app_id,auth_organization_id,fingerprint,receipt,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: ProductOnboardingGet :one
SELECT * FROM product_control_onboardings WHERE account_id=$1 AND app_id=$2 AND id=$3;

-- name: ProductOnboardingLatest :one
SELECT * FROM product_control_onboardings WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3
ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: ProductOnboardingAccess :exec
UPDATE product_control_onboardings o SET access_verified_at=sqlc.arg(observed_at)
FROM apps a WHERE o.account_id=sqlc.arg(account_id) AND o.app_id=sqlc.arg(app_id) AND o.tenant_id=sqlc.arg(tenant_id)
AND o.auth_organization_id=sqlc.arg(organization_id) AND o.access_verified_at IS NULL
AND a.id=o.auth_app_id AND a.account_id=o.account_id AND a.slug=sqlc.arg(auth_app)::text AND a.status<>'deleted';

-- name: ProductOnboardingFirstSuccess :one
SELECT operation_id,feature,resolved_at FROM product_control_receipts
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND admitted_at >= sqlc.arg(since)::timestamptz
AND resolved_state='succeeded' AND resolved_at IS NOT NULL ORDER BY resolved_at,operation_id LIMIT 1;
