-- Projection of account/invoice fields read by the referral view.
CREATE TABLE accounts (id uuid PRIMARY KEY, email text NOT NULL, plan text NOT NULL, status text NOT NULL, email_verified_at timestamptz);
CREATE TABLE apps (id uuid PRIMARY KEY, account_id uuid NOT NULL);
CREATE TABLE invoices (id uuid PRIMARY KEY, account_id uuid NOT NULL, status text NOT NULL, currency text NOT NULL, amount_paid_cents bigint NOT NULL, subtotal_cents bigint NOT NULL, tax_cents bigint NOT NULL, amount_refunded_cents bigint NOT NULL, amount_refund_pending_cents bigint NOT NULL, created_at timestamptz NOT NULL);




CREATE TABLE referral_links (
 code text PRIMARY KEY CHECK (code ~ '^[A-Za-z0-9_-]{22}$'),
 owner_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('customer','sales')),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 UNIQUE (owner_id,kind)
);

CREATE TABLE referral_attributions (
 account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 code text NOT NULL REFERENCES referral_links(code) ON DELETE CASCADE,
 currency text NOT NULL CHECK (currency IN ('usd','eur')),
 customer_reward_cents bigint NOT NULL CHECK (customer_reward_cents BETWEEN 0 AND 100000000),
 referred_reward_cents bigint NOT NULL CHECK (referred_reward_cents BETWEEN 0 AND 100000000),
 commission_bps integer NOT NULL CHECK (commission_bps BETWEEN 0 AND 10000),
 hold_days integer NOT NULL CHECK (hold_days BETWEEN 0 AND 365),
 conversion_days integer NOT NULL CHECK (conversion_days BETWEEN 1 AND 365),
 first_paid_invoice_id uuid CHECK (first_paid_invoice_id IS NULL OR first_paid_invoice_id<>'00000000-0000-0000-0000-000000000000'),
 first_paid_at timestamptz CHECK (first_paid_at IS NULL OR isfinite(first_paid_at)),
 CHECK ((first_paid_invoice_id IS NULL)=(first_paid_at IS NULL)),
 stage text NOT NULL DEFAULT 'new' CHECK (stage IN ('new','contacted','qualified','lost')),
 note text NOT NULL DEFAULT '' CHECK (octet_length(note) <= 2000),
 follow_up date CHECK (follow_up IS NULL OR isfinite(follow_up)),
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
CREATE INDEX referral_attributions_link ON referral_attributions(code,account_id);

-- The first observed paid invoice is retained by the invoice writer in the
-- same transaction. Refunds never advance attribution to a later invoice.
CREATE VIEW sales_referral_overview AS
SELECT r.account_id,l.owner_id,l.kind,a.email,a.plan,a.status AS account_status,
 r.created_at,r.stage,r.note,r.follow_up,r.currency,
 (SELECT count(*) FROM apps ap WHERE ap.account_id=a.id) AS apps,
 COALESCE(i.status='paid',false)::boolean AS paid,
 COALESCE(i.amount_paid_cents,0)::bigint AS paid_cents,
 (r.first_paid_at + make_interval(days=>r.hold_days))::timestamptz AS eligible_at,
 CASE
  WHEN r.first_paid_at IS NULL THEN 'registered'
  WHEN i.id IS NULL THEN 'review'
  WHEN i.status<>'paid' OR i.amount_refunded_cents>0 OR i.amount_refund_pending_cents>0 THEN 'reversed'
  WHEN i.currency<>r.currency THEN 'currency_mismatch'
  WHEN a.status<>'active' OR a.email_verified_at IS NULL OR owner.status<>'active' THEN 'review'
  WHEN (l.kind='customer' AND r.customer_reward_cents=0 AND r.referred_reward_cents=0)
    OR (l.kind='sales' AND r.commission_bps=0) THEN 'unconfigured'
  WHEN now()<r.first_paid_at+make_interval(days=>r.hold_days) THEN 'holding'
  ELSE 'eligible'
 END AS reward_status,
 CASE WHEN l.kind='customer' THEN r.customer_reward_cents
  ELSE COALESCE(trunc(LEAST(i.subtotal_cents,GREATEST(i.amount_paid_cents-i.tax_cents,0))::numeric*r.commission_bps/10000)::bigint,0)
 END::bigint AS reward_cents,
 CASE WHEN l.kind='customer' THEN r.referred_reward_cents ELSE 0::bigint END AS referred_reward_cents
FROM referral_attributions r
JOIN referral_links l ON l.code=r.code
JOIN accounts a ON a.id=r.account_id
JOIN accounts owner ON owner.id=l.owner_id
LEFT JOIN invoices i ON i.id=r.first_paid_invoice_id AND i.account_id=r.account_id;
ALTER TABLE referral_links ADD COLUMN share_code text CHECK (share_code IS NULL OR share_code ~ '^[A-Z0-9]{6}$');
CREATE UNIQUE INDEX referral_links_share_code_unique ON referral_links(share_code) WHERE share_code IS NOT NULL;
