-- name: ProductPlanGetChange :one
SELECT fingerprint, receipt FROM product_control_plan_changes
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND id=$4;

-- name: ProductPlanInsertChange :exec
INSERT INTO product_control_plan_changes(id,account_id,app_id,tenant_id,fingerprint,receipt,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: ProductPlanListChanges :many
SELECT id,receipt,created_at FROM product_control_plan_changes
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3
AND (sqlc.arg(after_id)::text='' OR (created_at,id) < (sqlc.arg(after_at)::timestamptz,sqlc.narg(after_uuid)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: ProductPlanStarts :many
SELECT feature,count(*)::bigint AS starts FROM product_control_receipts
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND period=$4
AND feature=ANY(sqlc.arg(features)::text[]) GROUP BY feature ORDER BY feature;
