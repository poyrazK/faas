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
