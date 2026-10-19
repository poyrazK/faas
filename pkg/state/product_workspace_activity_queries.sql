-- Customer-safe fields are selected explicitly. Permission and realm filters
-- apply before pagination; owner notes, prices, snapshots and handoffs stay out.
-- name: ProductWorkspaceActivity :many
WITH events AS (
 SELECT ('work_'||r.resolved_state)::text AS kind,r.operation_id AS source_id,r.resolved_at AS occurred_at,
 r.feature,''::text AS help_kind,'{}'::jsonb AS details
 FROM product_control_receipts r
 WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id)
 AND sqlc.arg(can_view_work)::boolean AND sqlc.arg(category_filter)::text IN ('all','work')
 AND r.resolved_state<>'' AND r.resolved_at IS NOT NULL
 UNION ALL
 SELECT 'work_attention',r.operation_id,(o.record->>'updated_at')::timestamptz,r.feature,'','{}'::jsonb
 FROM product_control_receipts r JOIN customer_operations o
 ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
 WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id)
 AND sqlc.arg(can_view_work)::boolean AND sqlc.arg(category_filter)::text IN ('all','work')
 AND r.resolved_state='' AND o.state='requires_reconciliation' AND o.expires_at>sqlc.arg(observed_at)::timestamptz
 UNION ALL
 SELECT 'plan_changed',p.id,p.created_at,'','',jsonb_build_object('plan',jsonb_build_object(
 'before',p.receipt->'preview'->'before_customer'->>'plan','after',p.receipt->'preview'->'after_customer'->>'plan'))
 FROM product_control_plan_changes p
 WHERE p.account_id=sqlc.arg(account_id) AND p.app_id=sqlc.arg(app_id) AND p.tenant_id=sqlc.arg(tenant_id)
 AND sqlc.arg(can_manage_plan)::boolean AND sqlc.arg(category_filter)::text IN ('all','plans')
 AND p.receipt->'preview'->'before_customer'->>'auth_organization_id'=sqlc.arg(organization_id)::uuid::text
 AND p.receipt->'preview'->'after_customer'->>'auth_organization_id'=sqlc.arg(organization_id)::uuid::text
 UNION ALL
 SELECT CASE WHEN d.kind='credit_note' THEN 'credit_recorded' ELSE 'statement_finalized' END,d.id,d.created_at,'','',
 jsonb_build_object('billing',jsonb_build_object('period',d.period,'currency',d.currency,'amount_millicents',d.amount_millicents))
 FROM product_control_billing_documents d
 WHERE d.account_id=sqlc.arg(account_id) AND d.app_id=sqlc.arg(app_id) AND d.tenant_id=sqlc.arg(tenant_id)
 AND sqlc.arg(can_view_billing)::boolean AND sqlc.arg(category_filter)::text IN ('all','billing')
 UNION ALL
 SELECT CASE WHEN h.status='resolved' THEN 'help_resolved' ELSE 'help_replied' END,h.id,h.updated_at,'',h.kind,'{}'::jsonb
 FROM product_control_help_requests h
 WHERE h.account_id=sqlc.arg(account_id) AND h.app_id=sqlc.arg(app_id) AND h.tenant_id=sqlc.arg(tenant_id)
 AND h.auth_app_id=sqlc.arg(auth_app_id) AND h.auth_organization_id=sqlc.arg(organization_id)
 AND h.version>1 AND sqlc.arg(category_filter)::text IN ('all','help')
 AND (h.kind='workspace' OR (h.kind='run' AND sqlc.arg(can_view_work)::boolean) OR (h.kind='feature' AND sqlc.arg(can_view_feature)::boolean))
)
SELECT kind,source_id,occurred_at,feature,help_kind,details FROM events
WHERE sqlc.arg(after_id)::text='' OR (occurred_at,kind COLLATE "C",source_id) <
 (sqlc.arg(after_at)::timestamptz,sqlc.arg(after_kind)::text COLLATE "C",sqlc.narg(after_uuid)::uuid)
ORDER BY occurred_at DESC,kind COLLATE "C" DESC,source_id DESC LIMIT sqlc.arg(page_limit);
