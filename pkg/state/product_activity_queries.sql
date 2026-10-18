-- One statement observes all sources with one PostgreSQL snapshot. No customer
-- Operations or Auth journals are read; no signed reviews, inputs or result payloads are returned.
-- Source IDs identify retained receipts; kind disambiguates their milestones.
-- name: ProductCustomerActivity :many
WITH onboardings AS (
 SELECT o.* FROM product_control_onboardings o
 WHERE o.account_id=sqlc.arg(account_id) AND o.app_id=sqlc.arg(app_id) AND o.tenant_id=sqlc.arg(tenant_id)
), receipts AS (
 SELECT r.*,jsonb_build_object('revision',r.revision,'feature',r.feature,'plan',r.plan,'currency',r.currency,
  'period',r.period,'unit_price_millicents',r.unit_price_millicents,'admitted_at',r.admitted_at,
  'admission',nullif(r.admission_evidence,'{}'::jsonb)) AS terms
 FROM product_control_receipts r
 WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id)
), documents AS (
 SELECT d.*,jsonb_build_object('id',d.id,'kind',d.kind,'tenant_id',d.tenant_id,'period',d.period,'currency',d.currency,
  'amount_millicents',d.amount_millicents,'statement_id',d.statement_id,'operation_id',d.operation_id,
  'reason',d.reason,'created_at',d.created_at) AS document
 FROM product_control_billing_documents d
 WHERE d.account_id=sqlc.arg(account_id) AND d.app_id=sqlc.arg(app_id) AND d.tenant_id=sqlc.arg(tenant_id)
), events AS (
 SELECT 'onboarding_completed'::text AS kind,id AS source_id,created_at AS occurred_at,
  jsonb_build_object('assignment',jsonb_build_object('after',receipt->'customer')) AS details FROM onboardings
 UNION ALL
 SELECT 'workspace_visit_observed',id,access_verified_at,'{}'::jsonb FROM onboardings WHERE access_verified_at IS NOT NULL
 UNION ALL
 SELECT 'onboarding_first_success',o.id,r.resolved_at,
  jsonb_build_object('milestone',jsonb_build_object('operation_id',r.operation_id,'feature',r.feature,'verified_at',r.resolved_at))
 FROM onboardings o CROSS JOIN LATERAL (
  SELECT operation_id,feature,resolved_at FROM receipts
  WHERE admitted_at>=o.created_at AND resolved_state='succeeded' AND resolved_at IS NOT NULL
  ORDER BY resolved_at,operation_id LIMIT 1
 ) r
 UNION ALL
 SELECT 'customer_assignment_changed',c.id,c.created_at,
  jsonb_build_object('assignment',jsonb_build_object('before',c.receipt->'before','after',c.receipt->'after'))
 FROM product_control_customer_changes c
 WHERE c.account_id=sqlc.arg(account_id) AND c.app_id=sqlc.arg(app_id) AND c.tenant_id=sqlc.arg(tenant_id)
 UNION ALL
 SELECT 'customer_plan_changed',p.id,p.created_at,
  jsonb_build_object('assignment',jsonb_build_object('before',p.receipt->'preview'->'before_customer',
   'after',p.receipt->'preview'->'after_customer','actor_role',p.receipt->>'actor_role'))
 FROM product_control_plan_changes p
 WHERE p.account_id=sqlc.arg(account_id) AND p.app_id=sqlc.arg(app_id) AND p.tenant_id=sqlc.arg(tenant_id)
 UNION ALL
 SELECT 'work_admitted',operation_id,admitted_at,jsonb_build_object('terms',terms) FROM receipts
 UNION ALL
 SELECT 'work_resolved',operation_id,resolved_at,jsonb_build_object('terms',terms,'outcome',resolved_state)
 FROM receipts WHERE resolved_state<>'' AND resolved_at IS NOT NULL
 UNION ALL
 SELECT 'charge_waived',r.operation_id,w.created_at,jsonb_build_object('terms',r.terms,'reason',w.reason)
 FROM receipts r JOIN product_control_waivers w USING(operation_id)
 UNION ALL
 SELECT 'billing_document_created',id,created_at,jsonb_build_object('document',document) FROM documents
 UNION ALL
 SELECT 'invoice_handoff_recorded',d.id,h.created_at,
  jsonb_build_object('period',d.period,'handoff',jsonb_build_object('document_id',h.document_id,
   'external_invoice_id',h.external_invoice_id,'currency',d.currency,'amount_millicents',d.amount_millicents,'created_at',h.created_at))
 FROM documents d JOIN product_control_invoice_handoffs h ON h.document_id=d.id AND h.account_id=d.account_id
)
SELECT kind,source_id,occurred_at,details FROM events
WHERE sqlc.arg(after_id)::text='' OR (occurred_at,kind COLLATE "C",source_id) <
 (sqlc.arg(after_at)::timestamptz,sqlc.arg(after_kind)::text COLLATE "C",sqlc.narg(after_uuid)::uuid)
ORDER BY occurred_at DESC,kind COLLATE "C" DESC,source_id DESC LIMIT sqlc.arg(page_limit);
