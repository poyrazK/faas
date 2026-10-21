-- Product Control customers of billing runs (ADR-20261018212918 §4).

-- Each assigned customer of an app with a published catalog that has receipts
-- in the run's month.
-- name: BillingRunDiscoverProductItems :exec
INSERT INTO billing_run_product_items(run_id,app_id,tenant_id,external_ref,next_attempt_at,updated_at)
SELECT DISTINCT sqlc.arg(run_id)::uuid,r.app_id,r.tenant_id,p.external_ref,sqlc.arg(now)::timestamptz,sqlc.arg(now)::timestamptz
FROM product_control_receipts r
JOIN product_control_heads h ON h.account_id=r.account_id AND h.app_id=r.app_id
JOIN product_control_customers c ON c.account_id=r.account_id AND c.app_id=r.app_id AND c.tenant_id=r.tenant_id
JOIN platform_tenants p ON p.account_id=r.account_id AND p.id=r.tenant_id
WHERE r.account_id=sqlc.arg(account_id) AND r.period=sqlc.arg(period)
ON CONFLICT DO NOTHING;

-- Each customer who held a plan with a monthly fee in the run's month, with
-- or without runs (ADR-20261021154511): the assignment in force when the
-- month starts, or one made during it.
-- name: BillingRunDiscoverProductFeeItems :exec
INSERT INTO billing_run_product_items(run_id,app_id,tenant_id,external_ref,next_attempt_at,updated_at)
SELECT DISTINCT sqlc.arg(run_id)::uuid,a.app_id,a.tenant_id,p.external_ref,sqlc.arg(now)::timestamptz,sqlc.arg(now)::timestamptz
FROM product_control_plan_history a
JOIN product_control_heads h ON h.account_id=a.account_id AND h.app_id=a.app_id
JOIN product_control_revisions v ON v.app_id=a.app_id AND v.revision=a.revision
JOIN platform_tenants p ON p.account_id=a.account_id AND p.id=a.tenant_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.effective_at < sqlc.arg(month_end)::timestamptz
  AND COALESCE((v.spec->'plans'->a.plan->>'monthly_fee_millicents')::bigint, 0) > 0
  AND NOT EXISTS (SELECT 1 FROM product_control_plan_history l
    WHERE l.app_id=a.app_id AND l.tenant_id=a.tenant_id AND l.effective_at > a.effective_at AND l.effective_at <= sqlc.arg(month_start)::timestamptz)
ON CONFLICT DO NOTHING;

-- name: BillingRunMarkProductDiscovered :exec
UPDATE billing_runs SET product_discovered_at=sqlc.arg(now)::timestamptz WHERE id=sqlc.arg(id);

-- Items are keyed product_control/<app>/<tenant>, after every other item kind.
-- name: BillingRunProductItems :many
SELECT i.*,coalesce((SELECT e.payment_status FROM usage_statement_invoice_exports e
  WHERE e.source_kind='product_control' AND e.statement_id=i.document_id),'')::text AS payment_status
FROM billing_run_product_items i
WHERE i.run_id=sqlc.arg(run_id) AND (sqlc.arg(outcome)::text='' OR i.outcome=sqlc.arg(outcome)::text)
 AND ('product_control/'||i.app_id::text||'/'||i.tenant_id::text) COLLATE "C">sqlc.arg(after)::text COLLATE "C"
ORDER BY ('product_control/'||i.app_id::text||'/'||i.tenant_id::text) COLLATE "C"
LIMIT sqlc.arg(page_limit);

-- name: BillingRunDueProductItems :many
SELECT i.*,coalesce((SELECT e.payment_status FROM usage_statement_invoice_exports e
  WHERE e.source_kind='product_control' AND e.statement_id=i.document_id),'')::text AS payment_status
FROM billing_run_product_items i
WHERE i.run_id=sqlc.arg(run_id) AND i.outcome='pending' AND i.next_attempt_at<=sqlc.arg(now)::timestamptz
ORDER BY i.app_id::text COLLATE "C" DESC,i.tenant_id::text COLLATE "C" DESC
LIMIT sqlc.arg(page_limit);

-- name: BillingRunUpdateProductItem :execrows
UPDATE billing_run_product_items i
SET outcome=sqlc.arg(outcome),document_id=sqlc.narg(document_id)::uuid,currency=sqlc.narg(currency)::text,
 amount_millicents=sqlc.narg(amount_millicents)::bigint,invoice_id=sqlc.narg(invoice_id)::text,exported_count=sqlc.arg(exported_count),
 reason=sqlc.narg(reason)::text,attempts=sqlc.arg(attempts),next_attempt_at=sqlc.arg(next_attempt_at),updated_at=now()
FROM billing_runs r
WHERE r.id=i.run_id AND r.lease_owner=sqlc.arg(owner)::text AND r.status<>'completed'
 AND i.run_id=sqlc.arg(run_id) AND i.app_id=sqlc.arg(app_id) AND i.tenant_id=sqlc.arg(tenant_id);

-- name: BillingRunProductCounts :many
SELECT run_id,outcome,count(*)::bigint AS n FROM billing_run_product_items
WHERE run_id=ANY(sqlc.arg(run_ids)::uuid[]) GROUP BY run_id,outcome;
