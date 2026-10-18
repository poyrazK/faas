-- name: ProductControlOwnedApp :one
SELECT id FROM apps WHERE account_id=$1 AND id=$2 AND status<>'deleted' FOR SHARE;

-- name: ProductControlPutRevision :exec
INSERT INTO product_control_revisions(account_id,app_id,revision,spec)
VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING;

-- name: ProductControlSetHead :exec
INSERT INTO product_control_heads(account_id,app_id,revision) VALUES ($1,$2,$3)
ON CONFLICT(app_id) DO UPDATE SET revision=EXCLUDED.revision;

-- name: ProductControlGetHead :one
SELECT r.revision,r.spec FROM product_control_heads h
JOIN product_control_revisions r USING(app_id,revision)
WHERE h.account_id=$1 AND h.app_id=$2;

-- name: ProductControlGetRevision :one
SELECT spec FROM product_control_revisions WHERE account_id=$1 AND app_id=$2 AND revision=$3;

-- name: ProductControlManagedApp :one
SELECT EXISTS(SELECT 1 FROM product_control_heads WHERE account_id=$1 AND app_id=$2)::boolean;

-- name: ProductControlSetCustomer :exec
INSERT INTO product_control_customers(account_id,app_id,tenant_id,revision,plan,suspended,auth_organization_id)
VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(app_id,tenant_id) DO UPDATE
SET revision=EXCLUDED.revision,plan=EXCLUDED.plan,suspended=EXCLUDED.suspended,auth_organization_id=EXCLUDED.auth_organization_id;

-- name: ProductControlGetCustomer :one
SELECT revision,plan,suspended,auth_organization_id FROM product_control_customers WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3;

-- name: ProductControlCustomerByAuthOrganization :one
SELECT tenant_id,revision,plan,suspended,auth_organization_id FROM product_control_customers c
JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
WHERE c.account_id=$1 AND c.app_id=$2 AND c.auth_organization_id=$3 AND a.status<>'deleted';

-- name: ProductControlListCustomers :many
SELECT tenant_id,revision,plan,suspended,auth_organization_id FROM product_control_customers
WHERE account_id=$1 AND app_id=$2 AND tenant_id::text>sqlc.arg(cursor)::text
ORDER BY tenant_id::text LIMIT 101;

-- name: ProductControlCountStarts :one
SELECT count(*)::bigint FROM product_control_receipts
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND feature=$4 AND period=$5;

-- name: ProductControlInsertReceipt :exec
INSERT INTO product_control_receipts(operation_id,account_id,app_id,tenant_id,revision,feature,plan,currency,period,unit_price_millicents,admitted_at,admission_evidence)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);

-- name: ProductControlSettleReceipt :exec
UPDATE product_control_receipts SET resolved_state=sqlc.arg(resolved_state), resolved_at=sqlc.arg(resolved_at),
succeeded_at=CASE WHEN sqlc.arg(resolved_state)::text='succeeded' THEN sqlc.arg(resolved_at) ELSE succeeded_at END
WHERE operation_id=sqlc.arg(operation_id) AND resolved_state='';

-- name: ProductControlUsage :many
SELECT feature,revision,plan,count(*)::bigint AS starts,
count(succeeded_at)::bigint AS succeeded,
sum(CASE WHEN succeeded_at IS NOT NULL AND w.operation_id IS NULL THEN unit_price_millicents ELSE 0 END)::bigint AS amount_millicents
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4
GROUP BY feature,revision,plan ORDER BY feature,revision,plan;

-- name: ProductControlLiveDefinition :one
SELECT od.id::text FROM customer_operation_definitions od
JOIN deployments d ON d.id=od.deployment_id AND d.app_id=od.app_id AND d.scope=od.scope
JOIN apps a ON a.id=d.app_id AND a.account_id=od.account_id
WHERE od.account_id=$1 AND od.app_id=$2 AND od.scope=$3 AND od.name=$4
AND a.status<>'deleted' AND d.status='live' AND d.traffic_percent=100
AND NOT EXISTS(SELECT 1 FROM deployments sibling WHERE sibling.app_id=d.app_id
 AND sibling.scope=d.scope AND sibling.id<>d.id AND sibling.status='live' AND sibling.traffic_percent>0);

-- name: ProductControlGetReceipt :one
SELECT r.*,w.reason AS waiver_reason,w.created_at AS waived_at,o.record AS operation_record FROM product_control_receipts r
JOIN apps a ON a.id=r.app_id AND a.account_id=r.account_id
LEFT JOIN product_control_waivers w USING(operation_id)
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id
 AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
WHERE r.account_id=$1 AND r.app_id=$2 AND r.operation_id=$3 AND a.status<>'deleted';
