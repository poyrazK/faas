-- name: ProductOwnerGetChange :one
SELECT fingerprint,receipt FROM product_control_customer_changes
WHERE id=$1 AND account_id=$2 AND app_id=$3 AND tenant_id=$4;

-- name: ProductOwnerInsertChange :exec
INSERT INTO product_control_customer_changes(id,account_id,app_id,tenant_id,fingerprint,receipt,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: ProductOwnerRuns :many
SELECT r.*,w.reason AS waiver_reason,w.created_at AS waived_at,
 coalesce(CASE WHEN o.state IN ('accepted','running') OR o.expires_at > sqlc.arg(now)::timestamptz THEN o.state END,'')::text AS state,
 (SELECT coalesce(-sum(d.amount_millicents),0)::bigint FROM product_control_billing_documents d
  WHERE d.operation_id=r.operation_id AND d.account_id=r.account_id AND d.app_id=r.app_id AND d.kind='credit_note') AS credited_millicents
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4
 AND (sqlc.arg(after_id)::text='' OR (r.admitted_at,r.operation_id) < (sqlc.arg(after_at)::timestamptz,sqlc.narg(after_uuid)::uuid))
ORDER BY r.admitted_at DESC,r.operation_id DESC LIMIT sqlc.arg(page_limit);

-- name: ProductOwnerAttention :many
WITH pending AS (
 SELECT r.tenant_id,r.period,
  count(*) FILTER (WHERE r.resolved_state='' AND w.operation_id IS NULL)::bigint AS unresolved,
  count(*) FILTER (WHERE r.resolved_state='' AND w.operation_id IS NULL AND
   (o.id IS NULL OR o.state NOT IN ('accepted','running')))::bigint AS needs_review
 FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
 LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
 WHERE r.account_id=$1 AND r.app_id=$2
 AND NOT EXISTS(SELECT 1 FROM product_control_billing_documents d WHERE d.account_id=r.account_id AND d.app_id=r.app_id AND d.tenant_id=r.tenant_id AND d.period=r.period AND d.kind='statement')
 GROUP BY r.tenant_id,r.period
)
SELECT tenant_id,period,unresolved,needs_review FROM pending
WHERE (period < sqlc.arg(current_period)::text OR needs_review > 0)
 AND (sqlc.arg(after_period)::text='' OR (period,tenant_id) > (sqlc.arg(after_period)::text,sqlc.narg(after_tenant)::uuid))
ORDER BY period,tenant_id LIMIT sqlc.arg(page_limit);

-- name: ProductCustomerWorkSummary :one
SELECT count(*)::bigint AS unresolved,
 count(*) FILTER (WHERE o.id IS NULL OR o.state NOT IN ('accepted','running'))::bigint AS needs_review,
 coalesce(min(r.period) FILTER (WHERE o.id IS NULL OR o.state NOT IN ('accepted','running')),'')::text AS review_period
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3
AND r.resolved_state='' AND w.operation_id IS NULL;
