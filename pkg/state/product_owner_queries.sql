-- name: ProductOwnerGetChange :one
SELECT fingerprint,receipt,schedule_status FROM product_control_customer_changes
WHERE id=$1 AND account_id=$2 AND app_id=$3 AND tenant_id=$4;

-- name: ProductOwnerInsertChange :exec
INSERT INTO product_control_customer_changes(id,account_id,app_id,tenant_id,fingerprint,receipt,created_at,scheduled_for,schedule_status)
VALUES($1,$2,$3,$4,$5,$6,$7,sqlc.narg(scheduled_for),sqlc.narg(schedule_status));

-- name: ProductOwnerLatestSchedule :one
SELECT receipt,schedule_status FROM product_control_customer_changes
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND scheduled_for IS NOT NULL
ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: ProductOwnerDueSchedule :one
SELECT id,receipt FROM product_control_customer_changes
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND schedule_status='pending' AND scheduled_for<=$4;

-- name: ProductOwnerDueScheduleScopes :many
SELECT c.account_id,c.app_id,c.tenant_id FROM product_control_customer_changes c
JOIN apps a ON a.id=c.app_id AND a.status<>'deleted' AND account_may_see_org_resources(c.account_id, a.org_id)
JOIN platform_tenants p ON p.id=c.tenant_id AND p.account_id=c.account_id
WHERE c.schedule_status='pending' AND c.scheduled_for<=$1
ORDER BY c.scheduled_for,c.id LIMIT $2;

-- name: ProductOwnerResolveSchedule :execrows
UPDATE product_control_customer_changes SET schedule_status=$5
WHERE id=$1 AND account_id=$2 AND app_id=$3 AND tenant_id=$4 AND schedule_status='pending';

-- name: ProductOwnerPlanChangedAfter :one
SELECT EXISTS (
    SELECT FROM product_control_plan_history WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND effective_at>$4
)::boolean;

-- name: ProductOwnerRuns :many
SELECT r.*,w.reason AS waiver_reason,w.created_at AS waived_at,
 coalesce(CASE WHEN o.state IN ('accepted','running','waiting') OR o.expires_at > sqlc.arg(now)::timestamptz
  OR o.record->'data_repair'->>'phase' IN ('applying','outcome_unknown','replacing','resolving') THEN o.state END,'')::text AS state,
 (SELECT coalesce(-sum(d.amount_millicents),0)::bigint FROM product_control_billing_documents d
  WHERE d.operation_id=r.operation_id AND d.account_id=r.account_id AND d.app_id=r.app_id AND d.kind='credit_note') AS credited_millicents
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4
 AND (NOT sqlc.arg(review_only)::boolean OR (r.resolved_state='' AND w.operation_id IS NULL AND
  (r.period < sqlc.arg(current_period)::text OR o.id IS NULL OR o.state NOT IN ('accepted','running','waiting'))))
 AND (sqlc.arg(after_id)::text='' OR (r.admitted_at,r.operation_id) < (sqlc.arg(after_at)::timestamptz,sqlc.narg(after_uuid)::uuid))
ORDER BY r.admitted_at DESC,r.operation_id DESC LIMIT sqlc.arg(page_limit);

-- name: ProductOwnerAttention :many
WITH pending AS (
 SELECT r.tenant_id,r.period,
  count(*) FILTER (WHERE r.resolved_state='' AND w.operation_id IS NULL)::bigint AS unresolved,
  count(*) FILTER (WHERE r.resolved_state='' AND w.operation_id IS NULL AND
   (o.id IS NULL OR o.state NOT IN ('accepted','running','waiting')))::bigint AS needs_review
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
 count(*) FILTER (WHERE o.id IS NULL OR o.state NOT IN ('accepted','running','waiting'))::bigint AS needs_review,
 coalesce(min(r.period) FILTER (WHERE o.id IS NULL OR o.state NOT IN ('accepted','running','waiting')),'')::text AS review_period
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3
AND r.resolved_state='' AND w.operation_id IS NULL;

-- name: ProductOwnerBillingQueueCandidates :many
-- Discover possible statements from both runs and paid terms held during a
-- closed month. The canonical billing review prices trials and proration and
-- rechecks finalization before an item is displayed.
WITH assignments AS (
 SELECT tenant_id,plan,revision,effective_at
 FROM product_control_plan_history
 WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
   AND (sqlc.narg(after_tenant)::uuid IS NULL OR tenant_id>sqlc.narg(after_tenant)::uuid)
   AND effective_at<sqlc.arg(month_end)::timestamptz
 UNION ALL
 SELECT tenant_id,receipt->'after'->>'plan',receipt->'after'->>'revision',scheduled_for
 FROM product_control_customer_changes
 WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
   AND (sqlc.narg(after_tenant)::uuid IS NULL OR tenant_id>sqlc.narg(after_tenant)::uuid)
   AND schedule_status='pending' AND scheduled_for<sqlc.arg(month_end)::timestamptz
), candidates AS (
 SELECT r.tenant_id FROM product_control_receipts r
 WHERE r.account_id=sqlc.arg(account_id)::uuid AND r.app_id=sqlc.arg(app_id)::uuid
   AND r.period=sqlc.arg(period)::text
   AND (sqlc.narg(after_tenant)::uuid IS NULL OR r.tenant_id>sqlc.narg(after_tenant)::uuid)
 UNION
 SELECT a.tenant_id FROM assignments a
 JOIN product_control_revisions v ON v.app_id=sqlc.arg(app_id)::uuid AND v.revision=a.revision
 WHERE COALESCE((v.spec->'plans'->a.plan->>'monthly_fee_millicents')::bigint, 0)>0
   AND NOT EXISTS (
    SELECT 1 FROM product_control_plan_history l
    WHERE l.account_id=sqlc.arg(account_id)::uuid AND l.app_id=sqlc.arg(app_id)::uuid
      AND l.tenant_id=a.tenant_id AND l.effective_at>a.effective_at AND l.effective_at<=sqlc.arg(month_start)::timestamptz
   )
)
SELECT p.id AS tenant_id,p.name FROM candidates c
JOIN platform_tenants p ON p.id=c.tenant_id AND p.account_id=sqlc.arg(account_id)::uuid
WHERE NOT EXISTS (
 SELECT 1 FROM product_control_billing_documents d
 WHERE d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
   AND d.tenant_id=p.id AND d.period=sqlc.arg(period)::text AND d.kind='statement'
)
ORDER BY p.id LIMIT sqlc.arg(page_limit);

-- name: ProductOwnerInvoiceQueue :many
-- Include unfinished exports even if they already recorded their own
-- handoff and still need to mark the export complete.
SELECT d.id AS document_id,d.tenant_id,p.name,d.currency,d.amount_millicents,
       COALESCE(e.provider,'')::text AS provider
FROM product_control_billing_documents d
JOIN platform_tenants p ON p.id=d.tenant_id AND p.account_id=d.account_id
LEFT JOIN product_control_invoice_handoffs h ON h.document_id=d.id AND h.account_id=d.account_id
LEFT JOIN usage_statement_invoice_exports e
  ON e.statement_id=d.id AND e.account_id=d.account_id AND e.source_kind='product_control'
WHERE d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
  AND d.period=sqlc.arg(period)::text AND d.kind='statement' AND d.amount_millicents>0
  AND (sqlc.narg(after_document)::uuid IS NULL OR d.id>sqlc.narg(after_document)::uuid)
  AND e.status IS DISTINCT FROM 'completed'
  AND (h.document_id IS NULL OR (e.status='creating' AND e.provider_invoice_id=h.external_invoice_id))
ORDER BY d.id
LIMIT sqlc.arg(page_limit);

-- name: ProductOwnerPaymentQueue :many
WITH follow_up AS (
 SELECT d.id AS document_id,d.tenant_id,d.period,p.name,e.currency,e.amount_remaining_minor,
        e.due_at,e.status_synced_at,COALESCE(e.hosted_invoice_url,'')::text AS hosted_invoice_url,
        COALESCE(e.provider='paddle' AND e.payment_status='draft', false)::boolean AS paddle_draft,
        COALESCE(e.provider_invoice_id,'')::text AS provider_invoice_id,e.livemode,
        COALESCE(a.stage='sent' AND e.payment_status='open' AND e.due_at<sqlc.arg(as_of)::timestamptz
                 AND e.amount_remaining_minor>0,false)::boolean AS stripe_reminder,
        a.reminder_started_at,a.reminder_sent_at,
        COALESCE(e.provider='stripe' AND e.collection='none' AND
                 (e.payment_status='draft' OR a.stage IN ('preparing','finalizing','sending')) AND
                 a.stage IS DISTINCT FROM 'sent', false)::boolean AS stripe_send,
        (CASE
         WHEN a.stage='sending' AND a.send_started_at<=sqlc.arg(stripe_retry_before)::timestamptz THEN 'stripe_review'
         WHEN a.stage IN ('preparing','finalizing','sending') THEN 'stripe_send'
         WHEN COALESCE(e.status_synced_at,e.completed_at,e.created_at)<sqlc.arg(stale_before)::timestamptz THEN 'stale'
         WHEN e.payment_status='draft' AND a.stage IS DISTINCT FROM 'sent'
              AND (e.status_synced_at IS NOT NULL OR e.collection='none') THEN 'draft'
         WHEN e.payment_status='open' AND e.due_at<sqlc.arg(as_of)::timestamptz THEN 'overdue'
         ELSE ''
        END)::text AS action
 FROM product_control_billing_documents d
 JOIN platform_tenants p ON p.id=d.tenant_id AND p.account_id=d.account_id
 JOIN usage_statement_invoice_exports e
   ON e.statement_id=d.id AND e.account_id=d.account_id AND e.source_kind='product_control'
 LEFT JOIN product_control_stripe_invoice_sends a ON a.export_id=e.id
 WHERE d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
   AND d.kind='statement' AND e.status='completed'
   AND (e.payment_status IS NULL OR e.payment_status='draft'
        OR (e.payment_status IN ('open','uncollectible') AND COALESCE(e.amount_remaining_minor,1)>0))
   AND (sqlc.narg(after_document)::uuid IS NULL
        OR (d.period,d.id)>(sqlc.arg(after_period)::text,sqlc.narg(after_document)::uuid))
)
SELECT * FROM follow_up WHERE action<>''
ORDER BY period,document_id
LIMIT sqlc.arg(page_limit);

-- name: ProductStripeInvoiceSend :one
SELECT a.review_hash, a.terms_hash, a.due_at, a.stage, a.send_started_at,
       COALESCE(a.send_event_id,'')::text AS send_event_id, a.send_event_at,
       COALESCE(a.reminder_hash,'')::text AS reminder_hash, a.reminder_started_at, a.reminder_sent_at
FROM product_control_stripe_invoice_sends a
JOIN usage_statement_invoice_exports e ON e.id = a.export_id
WHERE e.account_id = sqlc.arg(account_id) AND e.id = sqlc.arg(export_id);

-- name: BeginProductStripeInvoiceSend :execrows
INSERT INTO product_control_stripe_invoice_sends (export_id, review_hash, terms_hash, due_at, stage)
SELECT e.id, sqlc.arg(review_hash), sqlc.arg(terms_hash), sqlc.arg(due_at), 'preparing'
FROM usage_statement_invoice_exports e
WHERE e.account_id = sqlc.arg(account_id) AND e.id = sqlc.arg(export_id)
  AND e.claim_token = sqlc.arg(token) AND e.claimed_until > now()
  AND e.provider = 'stripe' AND e.source_kind = 'product_control' AND e.collection = 'none'
  AND e.status = 'completed' AND e.provider_invoice_id IS NOT NULL
ON CONFLICT (export_id) DO NOTHING;

-- name: AdvanceProductStripeInvoiceSend :execrows
UPDATE product_control_stripe_invoice_sends a
SET stage = sqlc.arg(next_stage)::text,
    send_started_at = CASE WHEN sqlc.arg(next_stage)::text = 'sending' THEN sqlc.arg(observed_at)::timestamptz ELSE a.send_started_at END
FROM usage_statement_invoice_exports e
WHERE a.export_id = e.id AND e.account_id = sqlc.arg(account_id) AND e.id = sqlc.arg(export_id)
  AND e.claim_token = sqlc.arg(token) AND e.claimed_until > now()
  AND a.stage = sqlc.arg(previous_stage);

-- name: VerifyProductStripeInvoiceSend :execrows
UPDATE product_control_stripe_invoice_sends a
SET stage = 'sent', send_event_id = sqlc.arg(event_id), send_event_at = sqlc.arg(event_at)
FROM usage_statement_invoice_exports e
WHERE a.export_id = e.id AND e.account_id = sqlc.arg(account_id) AND e.id = sqlc.arg(export_id)
  AND e.claim_token = sqlc.arg(token) AND e.claimed_until > now()
  AND a.stage = 'sending' AND a.send_started_at <= sqlc.arg(retry_before)::timestamptz
  AND sqlc.arg(event_at)::timestamptz >= date_trunc('second', a.send_started_at);

-- name: BeginProductStripeInvoiceReminder :execrows
UPDATE product_control_stripe_invoice_sends a
SET reminder_hash = sqlc.arg(review_hash)::text, reminder_started_at = sqlc.arg(started_at)::timestamptz
FROM usage_statement_invoice_exports e
WHERE a.export_id = e.id AND e.account_id = sqlc.arg(account_id) AND e.id = sqlc.arg(export_id)
  AND e.claim_token = sqlc.arg(token) AND e.claimed_until > now()
  AND a.stage = 'sent' AND a.reminder_hash IS NULL;

-- name: CompleteProductStripeInvoiceReminder :execrows
UPDATE product_control_stripe_invoice_sends a
SET reminder_sent_at = GREATEST(a.reminder_started_at, sqlc.arg(sent_at)::timestamptz)
FROM usage_statement_invoice_exports e
WHERE a.export_id = e.id AND e.account_id = sqlc.arg(account_id) AND e.id = sqlc.arg(export_id)
  AND e.claim_token = sqlc.arg(token) AND e.claimed_until > now()
  AND a.stage = 'sent' AND a.reminder_hash = sqlc.arg(review_hash)::text AND a.reminder_sent_at IS NULL;

-- name: ProductOwnerOutstandingBalances :one
WITH balances AS (
 SELECT d.id AS document_id,d.tenant_id,d.period,p.name,e.currency,e.payment_status,
        e.amount_remaining_minor,e.due_at,e.status_synced_at,
        COALESCE(e.hosted_invoice_url,'')::text AS hosted_invoice_url,
        COALESCE(e.provider_invoice_id,'')::text AS provider_invoice_id,
        COALESCE(e.due_at,'9999-12-31 00:00:00+00'::timestamptz) AS sort_due,
        (e.status_synced_at IS NULL OR e.status_synced_at<sqlc.arg(stale_before)::timestamptz
         OR e.amount_remaining_minor IS NULL) AS needs_refresh,
        COALESCE(a.stage='sent' AND e.payment_status='open' AND e.due_at<sqlc.arg(as_of)::timestamptz
                 AND e.amount_remaining_minor>0,false)::boolean AS stripe_reminder,
        a.reminder_started_at,a.reminder_sent_at,
        (CASE
         WHEN e.payment_status='uncollectible' THEN 'uncollectible'
         WHEN e.due_at IS NULL THEN 'no_due_date'
         WHEN e.due_at<sqlc.arg(older_than)::timestamptz THEN 'over_30'
         WHEN e.due_at<sqlc.arg(today)::timestamptz THEN 'overdue'
         ELSE 'current'
        END)::text AS bucket
 FROM product_control_billing_documents d
 JOIN platform_tenants p ON p.id=d.tenant_id AND p.account_id=d.account_id
 JOIN usage_statement_invoice_exports e
   ON e.statement_id=d.id AND e.account_id=d.account_id AND e.source_kind='product_control'
 LEFT JOIN product_control_stripe_invoice_sends a ON a.export_id=e.id
 WHERE d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
   AND d.kind='statement' AND e.status='completed' AND e.livemode=sqlc.arg(livemode)::boolean
   AND e.payment_status IN ('open','uncollectible') AND COALESCE(e.amount_remaining_minor,1)>0
), totals AS (
 SELECT currency,bucket,count(*) AS invoice_count,COALESCE(sum(amount_remaining_minor),0)::text AS remaining_minor,
        count(*) FILTER (WHERE needs_refresh) AS refresh_count,
        count(*) FILTER (WHERE amount_remaining_minor IS NULL) AS unknown_count
 FROM balances GROUP BY currency,bucket
), invoice_page AS (
 SELECT * FROM balances
 WHERE sqlc.arg(bucket)::text<>'' AND bucket=sqlc.arg(bucket)::text AND currency=sqlc.arg(currency)::text
   AND (sqlc.narg(after_id)::uuid IS NULL
        OR (sort_due,document_id)>(sqlc.narg(after_due)::timestamptz,sqlc.narg(after_id)::uuid))
 ORDER BY sort_due,document_id
 LIMIT sqlc.arg(page_limit)
)
SELECT COALESCE((SELECT jsonb_agg(t ORDER BY t.currency,t.bucket) FROM totals t),'[]'::jsonb)::jsonb AS balance_totals,
       COALESCE((SELECT jsonb_agg(i ORDER BY i.sort_due,i.document_id) FROM invoice_page i),'[]'::jsonb)::jsonb AS balance_invoices;
