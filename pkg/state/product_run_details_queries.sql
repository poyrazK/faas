-- Select only customer run metadata. Retention matches Operations: active work
-- remains readable, while stopped work expires. Never return record payloads.
-- name: ProductRunDetails :one
SELECT r.feature,r.plan,r.period,r.admitted_at,r.resolved_state,r.resolved_at,
 (CASE WHEN r.admission_evidence IS NOT NULL AND r.admission_evidence<>'null'::jsonb
 THEN jsonb_build_object('monthly_starts',r.admission_evidence->'monthly_starts','starts_before',r.admission_evidence->'starts_before') ELSE NULL END)::jsonb AS allowance,
 coalesce(o.state,'')::text AS live_state,
 coalesce(o.state='succeeded' AND (coalesce(o.record->'result','null'::jsonb)<>'null'::jsonb OR coalesce(jsonb_array_length(o.record->'artifacts'),0)>0),false)::boolean AS result_available,
 CASE WHEN sqlc.arg(can_view_billing)::boolean THEN r.currency ELSE '' END::text AS currency,
 CASE WHEN sqlc.arg(can_view_billing)::boolean THEN r.unit_price_millicents ELSE 0 END::bigint AS unit_price_millicents,
 (sqlc.arg(can_view_billing)::boolean AND w.operation_id IS NOT NULL)::boolean AS waived
FROM product_control_receipts r
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
 AND (o.state IN ('accepted','running') OR o.expires_at>sqlc.arg(observed_at)::timestamptz)
LEFT JOIN product_control_waivers w ON w.operation_id=r.operation_id AND sqlc.arg(can_view_billing)::boolean
WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id) AND r.operation_id=sqlc.arg(operation_id);

-- One statement reference and signed aggregate, independent of document count.
-- No reasons, snapshots, invoice handoffs or unrelated operation credits.
-- name: ProductRunDetailsBilling :one
SELECT s.id AS statement_id,s.created_at AS statement_created_at,
 coalesce(c.amount,0)::bigint AS credit_millicents,coalesce(c.count,0)::bigint AS credit_count
FROM (SELECT 1) anchor
LEFT JOIN product_control_billing_documents s ON s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id)
 AND s.tenant_id=sqlc.arg(tenant_id) AND s.period=sqlc.arg(period) AND s.kind='statement'
LEFT JOIN LATERAL (
 SELECT sum(d.amount_millicents) AS amount,count(*)::bigint AS count FROM product_control_billing_documents d
 WHERE d.account_id=sqlc.arg(account_id) AND d.app_id=sqlc.arg(app_id) AND d.tenant_id=sqlc.arg(tenant_id)
 AND d.period=sqlc.arg(period) AND d.kind='credit_note' AND d.operation_id=sqlc.arg(operation_id) AND d.statement_id=s.id
) c ON true;
