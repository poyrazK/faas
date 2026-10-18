-- name: ProductHelpGet :one
SELECT * FROM product_control_help_requests
WHERE account_id=$1 AND app_id=$2 AND id=$3;

-- name: ProductHelpCount :one
SELECT count(*)::bigint AS total, count(*) FILTER (WHERE status <> 'resolved')::bigint AS active
FROM product_control_help_requests WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3;

-- name: ProductHelpInsert :exec
INSERT INTO product_control_help_requests
(id,account_id,app_id,tenant_id,auth_app_id,auth_organization_id,fingerprint,kind,request,context,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11);

-- name: ProductHelpList :many
SELECT * FROM product_control_help_requests
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id)
AND (sqlc.narg(tenant_filter)::uuid IS NULL OR tenant_id=sqlc.narg(tenant_filter))
AND (sqlc.arg(status_filter)::text='all' OR status=sqlc.arg(status_filter)
 OR (sqlc.arg(status_filter)='active' AND status <> 'resolved'))
AND (sqlc.arg(owner_mode)::boolean OR
 (auth_app_id=sqlc.narg(auth_app_filter) AND auth_organization_id=sqlc.narg(org_filter)
 AND (kind='workspace' OR (kind='feature' AND sqlc.arg(can_view_feature)::boolean)
 OR (kind='run' AND sqlc.arg(can_view_work)::boolean))))
AND (sqlc.arg(after_id)::text='' OR (created_at,id) < (sqlc.narg(after_at)::timestamptz,sqlc.narg(after_uuid)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: ProductHelpUpdate :execrows
UPDATE product_control_help_requests SET status=$4,response=$5,version=$6,updated_at=$7
WHERE account_id=$1 AND app_id=$2 AND id=$3 AND version=$8;
