-- name: EnsureLink :one
INSERT INTO referral_links(code,owner_id,kind,share_code) VALUES ($1,$2,$3,$4)
ON CONFLICT (owner_id,kind) DO UPDATE SET share_code=COALESCE(referral_links.share_code,EXCLUDED.share_code)
RETURNING *;

-- name: GetLink :one
SELECT l.* FROM referral_links l JOIN accounts a ON a.id=l.owner_id
WHERE l.share_code=$1 AND a.status='active';

-- name: OwnLink :one
SELECT * FROM referral_links WHERE owner_id=$1 AND kind=$2;

-- name: SetLinkActive :execrows
UPDATE referral_links SET active=$1
WHERE share_code=$2 AND (owner_id=sqlc.arg(owner_id)::uuid OR sqlc.arg(all_owners)::boolean);

-- name: AttributeSignup :execrows
INSERT INTO referral_attributions(account_id,code,currency,customer_reward_cents,referred_reward_cents,commission_bps,hold_days,conversion_days)
SELECT sqlc.arg(account_id)::uuid,l.code,sqlc.arg(currency)::text,
 sqlc.arg(customer_reward_cents)::bigint,sqlc.arg(referred_reward_cents)::bigint,
 sqlc.arg(commission_bps)::integer,sqlc.arg(hold_days)::integer,sqlc.arg(conversion_days)::integer
FROM referral_links l JOIN accounts a ON a.id=l.owner_id
WHERE l.share_code=sqlc.arg(code)::text AND l.active AND a.status='active' AND l.owner_id<>sqlc.arg(account_id)::uuid
ON CONFLICT (account_id) DO NOTHING;

-- name: RecordReferralPayment :exec
UPDATE referral_attributions r
SET first_paid_invoice_id=i.id,first_paid_at=now()
FROM invoices i
WHERE i.id=sqlc.arg(invoice_id)::uuid AND i.account_id=r.account_id
 AND i.status='paid' AND i.amount_paid_cents>0 AND r.first_paid_at IS NULL
 AND now()<=r.created_at+make_interval(days=>r.conversion_days);

-- name: ListReferrals :many
SELECT * FROM sales_referral_overview
WHERE (owner_id=sqlc.arg(owner_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (sqlc.arg(kind)::text='' OR kind=sqlc.arg(kind)::text)
 AND account_id>sqlc.arg(after_id)::uuid
ORDER BY account_id LIMIT sqlc.arg(row_limit)::integer;

-- name: ReferralSummary :many
SELECT kind,currency,count(*)::bigint AS signups,
 count(*) FILTER (WHERE paid)::bigint AS paid,
 count(*) FILTER (WHERE reward_status='eligible')::bigint AS eligible,
 COALESCE(sum(reward_cents) FILTER (WHERE reward_status='eligible'),0)::bigint AS reward_cents,
 COALESCE(sum(paid_cents) FILTER (WHERE paid AND reward_status NOT IN ('reversed','currency_mismatch')),0)::bigint AS paid_cents
FROM sales_referral_overview
WHERE (owner_id=sqlc.arg(owner_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (sqlc.arg(kind)::text='' OR kind=sqlc.arg(kind)::text)
GROUP BY kind,currency ORDER BY kind,currency;

-- name: UpdateLead :execrows
UPDATE referral_attributions r SET stage=$1,note=$2,follow_up=$3,updated_by=$4,updated_at=now()
FROM referral_links l
WHERE r.code=l.code AND r.account_id=$5
 AND (l.owner_id=sqlc.arg(owner_id)::uuid OR sqlc.arg(all_owners)::boolean);

-- name: SalesTeam :many
SELECT id,email FROM accounts
WHERE lower(email)=ANY(sqlc.arg(emails)::text[]) AND status='active'
 AND email_verified_at IS NOT NULL AND mfa_enrolled_at IS NOT NULL
ORDER BY lower(email),id;

-- name: ListProspects :many
SELECT l.*,a.email AS owner_email,
 EXISTS(SELECT 1 FROM sales_referral_overview v WHERE v.owner_id=l.owner_id AND v.kind='sales' AND lower(v.email)=l.email) AS registered,
 EXISTS(SELECT 1 FROM sales_referral_overview v WHERE v.owner_id=l.owner_id AND v.kind='sales' AND lower(v.email)=l.email AND v.paid) AS paid
FROM sales_leads l JOIN accounts a ON a.id=l.owner_id
WHERE (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (sqlc.arg(owner_filter)::text='' OR l.owner_id::text=sqlc.arg(owner_filter)::text)
 AND (sqlc.arg(stage)::text='' OR l.stage=sqlc.arg(stage)::text)
 AND position(lower(sqlc.arg(search)::text) in lower(l.name||' '||l.email||' '||l.company))>0
 AND (sqlc.arg(after_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR l.id<sqlc.arg(after_id)::uuid)
ORDER BY l.id DESC LIMIT sqlc.arg(row_limit)::integer;

-- name: ProspectCounts :many
SELECT stage,count(*)::bigint AS count FROM sales_leads l
WHERE (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (sqlc.arg(owner_filter)::text='' OR l.owner_id::text=sqlc.arg(owner_filter)::text)
 AND position(lower(sqlc.arg(search)::text) in lower(l.name||' '||l.email||' '||l.company))>0
GROUP BY stage ORDER BY stage;

-- name: CreateProspect :one
INSERT INTO sales_leads(id,owner_id,name,email,company,stage,note,created_by,updated_by)
SELECT sqlc.arg(id),a.id,sqlc.arg(name),sqlc.arg(email),sqlc.arg(company),sqlc.arg(stage),sqlc.arg(note),sqlc.arg(actor_id),sqlc.arg(actor_id)
FROM accounts a WHERE a.id=sqlc.arg(owner_id) AND a.status='active' AND a.email_verified_at IS NOT NULL AND a.mfa_enrolled_at IS NOT NULL
 AND (a.id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
RETURNING id;

-- name: UpdateProspect :one
UPDATE sales_leads l SET name=sqlc.arg(name),email=sqlc.arg(email),company=sqlc.arg(company),stage=sqlc.arg(stage),note=sqlc.arg(note),
 owner_id=sqlc.arg(owner_id),updated_by=sqlc.arg(actor_id),updated_at=now(),version=version+1
WHERE l.id=sqlc.arg(id) AND l.version=sqlc.arg(version)
 AND (sqlc.arg(owner_id)::uuid=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND EXISTS(SELECT 1 FROM accounts a WHERE a.id=sqlc.arg(owner_id) AND a.status='active' AND a.email_verified_at IS NOT NULL AND a.mfa_enrolled_at IS NOT NULL)
RETURNING id;

-- name: DeleteProspect :execrows
DELETE FROM sales_leads WHERE id=sqlc.arg(id) AND version=sqlc.arg(version)
 AND (owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean);

-- name: ListSalesTasks :many
SELECT t.id,t.lead_id,t.title,t.due_on,t.completed_at,t.version,l.name AS lead_name,l.email AS lead_email,l.owner_id,a.email AS owner_email
FROM sales_tasks t JOIN sales_leads l ON l.id=t.lead_id JOIN accounts a ON a.id=l.owner_id
WHERE (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (sqlc.arg(owner_filter)::text='' OR l.owner_id::text=sqlc.arg(owner_filter)::text)
 AND (sqlc.arg(status)::text='all' OR (sqlc.arg(status)::text='done')=(t.completed_at IS NOT NULL))
 AND (sqlc.arg(due_filter)::text='all'
  OR (sqlc.arg(due_filter)::text='overdue' AND t.completed_at IS NULL AND t.due_on<sqlc.arg(on_date)::date)
  OR (sqlc.arg(due_filter)::text='today' AND t.due_on=sqlc.arg(on_date)::date)
  OR (sqlc.arg(due_filter)::text='upcoming' AND t.due_on>sqlc.arg(on_date)::date))
 AND (t.due_on,t.id)>(sqlc.arg(after_date)::date,sqlc.arg(after_id)::uuid)
ORDER BY t.due_on,t.id LIMIT sqlc.arg(row_limit)::integer;

-- name: SalesTaskCounts :one
SELECT count(*)::bigint AS open,
 count(*) FILTER(WHERE t.due_on=sqlc.arg(on_date)::date)::bigint AS today,
 count(*) FILTER(WHERE t.due_on<sqlc.arg(on_date)::date)::bigint AS overdue
FROM sales_tasks t JOIN sales_leads l ON l.id=t.lead_id
WHERE t.completed_at IS NULL AND (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
 AND (sqlc.arg(owner_filter)::text='' OR l.owner_id::text=sqlc.arg(owner_filter)::text);

-- name: CreateSalesTask :one
INSERT INTO sales_tasks(id,lead_id,title,due_on,created_by,updated_by)
SELECT sqlc.arg(id),l.id,sqlc.arg(title),sqlc.arg(due_on),sqlc.arg(actor_id),sqlc.arg(actor_id)
FROM sales_leads l WHERE l.id=sqlc.arg(lead_id)
 AND (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
RETURNING id;

-- name: UpdateSalesTask :one
UPDATE sales_tasks t SET title=sqlc.arg(title),due_on=sqlc.arg(due_on),
 completed_at=CASE WHEN sqlc.arg(done)::boolean THEN COALESCE(t.completed_at,now()) ELSE NULL END,
 updated_by=sqlc.arg(actor_id),updated_at=now(),version=t.version+1
FROM sales_leads l WHERE l.id=t.lead_id AND t.id=sqlc.arg(id) AND t.version=sqlc.arg(version)
 AND (l.owner_id=sqlc.arg(actor_id)::uuid OR sqlc.arg(all_owners)::boolean)
RETURNING t.id;

-- name: SalesReportClock :one
SELECT transaction_timestamp()::timestamptz AS observed_at;

-- name: SalesReportComparison :many
WITH owners AS (
 SELECT id FROM accounts WHERE lower(email)=ANY(sqlc.arg(member_emails)::text[])
  AND status='active' AND email_verified_at IS NOT NULL AND mfa_enrolled_at IS NOT NULL
 UNION SELECT owner_id FROM sales_leads
 UNION SELECT owner_id FROM referral_links WHERE kind='sales'
), leads AS (
 SELECT owner_id,count(*)::bigint AS total,
 count(*) FILTER(WHERE stage='new')::bigint AS new,
 count(*) FILTER(WHERE stage='contacted')::bigint AS contacted,
 count(*) FILTER(WHERE stage='qualified')::bigint AS qualified,
 count(*) FILTER(WHERE stage='demo')::bigint AS demo,
 count(*) FILTER(WHERE stage='won')::bigint AS won,
 count(*) FILTER(WHERE stage='lost')::bigint AS lost
 FROM sales_leads
 WHERE (created_at AT TIME ZONE sqlc.arg(report_timezone)::text)::date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND (sqlc.arg(owner_filter)::text='' OR owner_id::text=sqlc.arg(owner_filter)::text)
 GROUP BY owner_id
), conversions AS (
 SELECT l.owner_id,count(*)::bigint AS signups,
 count(*) FILTER(WHERE r.first_paid_at IS NOT NULL)::bigint AS first_payments,
 floor(avg(extract(epoch FROM (r.first_paid_at-r.created_at))) FILTER(WHERE r.first_paid_at>=r.created_at))::bigint AS avg_first_payment_seconds,
 count(*) FILTER(WHERE r.first_paid_at IS NOT NULL AND i.id IS NULL)::bigint AS missing_invoices
 FROM referral_attributions r JOIN referral_links l ON l.code=r.code
 LEFT JOIN invoices i ON i.id=r.first_paid_invoice_id AND i.account_id=r.account_id
 WHERE l.kind='sales' AND (r.created_at AT TIME ZONE sqlc.arg(report_timezone)::text)::date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND (sqlc.arg(owner_filter)::text='' OR l.owner_id::text=sqlc.arg(owner_filter)::text)
 GROUP BY l.owner_id
)
SELECT a.id,a.email,
 COALESCE(lower(a.email)=ANY(sqlc.arg(member_emails)::text[]) AND a.status='active' AND a.email_verified_at IS NOT NULL AND a.mfa_enrolled_at IS NOT NULL,false)::boolean AS current_member,
 COALESCE(l.total,0)::bigint AS total,COALESCE(l.new,0)::bigint AS new,COALESCE(l.contacted,0)::bigint AS contacted,
 COALESCE(l.qualified,0)::bigint AS qualified,COALESCE(l.demo,0)::bigint AS demo,COALESCE(l.won,0)::bigint AS won,COALESCE(l.lost,0)::bigint AS lost,
 COALESCE(c.signups,0)::bigint AS signups,COALESCE(c.first_payments,0)::bigint AS first_payments,
 c.avg_first_payment_seconds,COALESCE(c.missing_invoices,0)::bigint AS missing_invoices
FROM owners o JOIN accounts a ON a.id=o.id
LEFT JOIN leads l ON l.owner_id=o.id LEFT JOIN conversions c ON c.owner_id=o.id
ORDER BY lower(a.email),a.id;

-- name: SalesReportCollections :many
SELECT l.owner_id,i.currency,count(*)::bigint AS invoices,
 sum(i.amount_paid_cents)::bigint AS paid_cents,
 sum(i.amount_refunded_cents)::bigint AS refunded_cents,
 sum(i.amount_refund_pending_cents)::bigint AS pending_refund_cents,
 sum(GREATEST(i.amount_paid_cents-i.amount_refunded_cents-i.amount_refund_pending_cents,0))::bigint AS retained_cents
FROM referral_attributions r JOIN referral_links l ON l.code=r.code
JOIN invoices i ON i.id=r.first_paid_invoice_id AND i.account_id=r.account_id
WHERE l.kind='sales' AND r.first_paid_at IS NOT NULL
 AND (r.created_at AT TIME ZONE sqlc.arg(report_timezone)::text)::date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
 AND (sqlc.arg(owner_filter)::text='' OR l.owner_id::text=sqlc.arg(owner_filter)::text)
GROUP BY l.owner_id,i.currency ORDER BY l.owner_id,i.currency;
