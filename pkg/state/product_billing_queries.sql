-- Account, shared app and tenant locks precede this; billing never locks operation,
-- invocation, deployment or pin rows. Exec discards IDs without materializing
-- an unbounded receipt list in Go. Settlement locks the same receipt rows.
-- name: ProductBillingLockReceipts :exec
SELECT operation_id FROM product_control_receipts
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND period=$4
ORDER BY operation_id FOR UPDATE;

-- name: ProductBillingLines :many
SELECT r.feature,r.revision,r.plan,r.unit_price_millicents,count(*)::bigint AS starts,
 count(*) FILTER (WHERE r.resolved_state='succeeded' AND w.operation_id IS NULL)::bigint AS succeeded,
 count(*) FILTER (WHERE r.resolved_state='failed' AND w.operation_id IS NULL)::bigint AS failed,
 count(*) FILTER (WHERE r.resolved_state='cancelled' AND w.operation_id IS NULL)::bigint AS cancelled,
 count(w.operation_id)::bigint AS waived,
 count(*) FILTER (WHERE r.resolved_state='' AND w.operation_id IS NULL)::bigint AS unresolved,
 sum(CASE WHEN r.resolved_state='succeeded' AND w.operation_id IS NULL THEN r.unit_price_millicents ELSE 0 END)::bigint AS amount_millicents,
 max(greatest(r.admitted_at,r.resolved_at,w.created_at))::timestamptz AS changed_at
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4
GROUP BY r.feature,r.revision,r.plan,r.unit_price_millicents
ORDER BY r.feature,r.revision,r.plan,r.unit_price_millicents LIMIT sqlc.arg(page_limit);

-- name: ProductBillingReceipts :many
SELECT r.*,w.reason AS waiver_reason,w.created_at AS waived_at
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4
 AND r.operation_id::text>sqlc.arg(cursor)::text
ORDER BY r.operation_id::text LIMIT sqlc.arg(page_limit);

-- name: ProductBillingReceipt :one
SELECT r.*,w.reason AS waiver_reason,w.created_at AS waived_at
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4 AND r.operation_id=$5;

-- name: ProductBillingWaive :exec
INSERT INTO product_control_waivers(operation_id,reason) VALUES($1,$2);

-- name: ProductBillingLockReceipt :exec
SELECT operation_id FROM product_control_receipts
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND period=$4 AND operation_id=$5 FOR UPDATE;

-- name: ProductBillingStatement :one
SELECT * FROM product_control_billing_documents
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND period=$4 AND kind='statement';

-- name: ProductBillingDocument :one
SELECT * FROM product_control_billing_documents
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND period=$4 AND id=$5;

-- name: ProductBillingDocuments :many
SELECT * FROM product_control_billing_documents
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND period=$4 AND id::text>sqlc.arg(cursor)::text
ORDER BY id::text LIMIT sqlc.arg(page_limit);

-- name: ProductBillingInsertDocument :exec
INSERT INTO product_control_billing_documents(id,account_id,app_id,tenant_id,period,kind,currency,amount_millicents,statement_id,operation_id,reason,snapshot,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);

-- name: ProductBillingCredited :one
SELECT coalesce(-sum(amount_millicents),0)::bigint FROM product_control_billing_documents
WHERE operation_id=$1 AND kind='credit_note';

-- name: ProductBillingGetHandoff :one
SELECT h.document_id,h.external_invoice_id,d.currency,d.amount_millicents,h.created_at
FROM product_control_invoice_handoffs h JOIN product_control_billing_documents d ON d.id=h.document_id
WHERE d.account_id=$1 AND d.app_id=$2 AND d.tenant_id=$3 AND d.period=$4 AND d.id=$5;

-- name: ProductBillingInsertHandoff :exec
INSERT INTO product_control_invoice_handoffs(document_id,account_id,external_invoice_id) VALUES($1,$2,$3);
-- name: ProductBillingScopeExists :one
SELECT EXISTS(SELECT 1 FROM apps a JOIN product_control_heads h ON h.app_id=a.id AND h.account_id=a.account_id
 JOIN platform_tenants t ON t.account_id=a.account_id
 WHERE a.account_id=$1 AND a.id=$2 AND t.id=$3 AND a.status<>'deleted')::boolean;
