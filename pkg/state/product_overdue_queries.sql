-- Product Control overdue policies and holds (ADR-20261018212918, mirroring ADR-1315).

-- name: ProductOverduePutPolicy :one
INSERT INTO product_control_overdue_policies(account_id,app_id,block_overdue_after_days)
SELECT a.account_id,a.id,sqlc.arg(days)::integer FROM apps a WHERE a.account_id=sqlc.arg(account_id) AND a.id=sqlc.arg(app_id)
ON CONFLICT (app_id) DO UPDATE SET block_overdue_after_days=excluded.block_overdue_after_days,updated_at=now()
RETURNING *;

-- name: ProductOverduePolicy :one
SELECT * FROM product_control_overdue_policies WHERE account_id=$1 AND app_id=$2;

-- name: ProductOverdueDeletePolicy :execrows
DELETE FROM product_control_overdue_policies WHERE account_id=$1 AND app_id=$2;

-- name: ProductOverdueDeleteAppHolds :many
DELETE FROM product_control_overdue_holds WHERE account_id=$1 AND app_id=$2 RETURNING *;

-- Policy deletion waits for a running recomputation; the recomputation skips
-- when another replica holds the lock.
-- name: ProductOverdueLock :exec
SELECT pg_advisory_xact_lock(hashtext('product_control_overdue_holds'));

-- name: ProductOverdueTryLock :one
SELECT pg_try_advisory_xact_lock(hashtext('product_control_overdue_holds'))::boolean AS locked;

-- Each customer's oldest exported statement that is still unpaid its app's
-- policy days after its due date. Drafts have no due date and never count.
-- name: ProductOverdueDue :many
SELECT DISTINCT ON (d.app_id,d.tenant_id) d.account_id,d.app_id,d.tenant_id,d.id AS document_id,e.id AS export_id,e.due_at::timestamptz AS due_at
FROM usage_statement_invoice_exports e
JOIN product_control_billing_documents d ON d.id=e.statement_id AND d.account_id=e.account_id AND d.kind='statement'
JOIN product_control_overdue_policies p ON p.app_id=d.app_id AND p.account_id=d.account_id
WHERE e.source_kind='product_control' AND e.status='completed'
 AND e.payment_status IN ('open','uncollectible') AND e.due_at IS NOT NULL
 AND coalesce(e.amount_remaining_minor,1)>0
 AND e.due_at+make_interval(days=>p.block_overdue_after_days)<=sqlc.arg(now)::timestamptz
ORDER BY d.app_id,d.tenant_id,e.due_at,e.id;

-- name: ProductOverdueAllHolds :many
SELECT * FROM product_control_overdue_holds ORDER BY app_id,tenant_id FOR UPDATE;

-- name: ProductOverdueUpsertHold :exec
INSERT INTO product_control_overdue_holds(app_id,tenant_id,account_id,document_id,export_id,due_at,since)
VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (app_id,tenant_id) DO UPDATE SET document_id=excluded.document_id,export_id=excluded.export_id,due_at=excluded.due_at;

-- name: ProductOverdueDeleteHold :exec
DELETE FROM product_control_overdue_holds WHERE app_id=$1 AND tenant_id=$2;

-- A held invoice's payment status is read at least every
-- api.ProductOverdueHeldSyncInterval, so paying it lifts the hold quickly.
-- name: ProductOverdueResyncHeld :exec
UPDATE usage_statement_invoice_exports e SET next_status_sync_at=sqlc.arg(soon)::timestamptz
WHERE e.next_status_sync_at>sqlc.arg(soon)::timestamptz AND e.id IN (SELECT export_id FROM product_control_overdue_holds);

-- Holds apply while their policy exists and their invoice is still unpaid, so
-- payment or a cleared policy takes effect before the next recomputation.
-- name: ProductOverdueHolds :many
SELECT h.*,coalesce(e.hosted_invoice_url,'')::text AS hosted_invoice_url
FROM product_control_overdue_holds h
JOIN product_control_overdue_policies p ON p.app_id=h.app_id AND p.account_id=h.account_id
JOIN usage_statement_invoice_exports e ON e.id=h.export_id
WHERE h.account_id=$1 AND h.app_id=$2
 AND e.payment_status IN ('open','uncollectible') AND coalesce(e.amount_remaining_minor,1)>0
ORDER BY h.due_at,h.tenant_id;

-- name: ProductOverdueCustomerHold :one
SELECT h.*,coalesce(e.hosted_invoice_url,'')::text AS hosted_invoice_url
FROM product_control_overdue_holds h
JOIN product_control_overdue_policies p ON p.app_id=h.app_id AND p.account_id=h.account_id
JOIN usage_statement_invoice_exports e ON e.id=h.export_id
WHERE h.account_id=$1 AND h.app_id=$2 AND h.tenant_id=$3
 AND e.payment_status IN ('open','uncollectible') AND coalesce(e.amount_remaining_minor,1)>0;
