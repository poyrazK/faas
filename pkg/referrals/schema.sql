-- Projection of account/invoice fields read by the referral view.
CREATE TABLE accounts (id uuid PRIMARY KEY, email text NOT NULL, plan text NOT NULL, status text NOT NULL, email_verified_at timestamptz, mfa_enrolled_at timestamptz);
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


SET LOCAL lock_timeout = '5s';

CREATE TABLE sales_leads (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 owner_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name=btrim(name) AND octet_length(name) BETWEEN 1 AND 120),
 email text NOT NULL CHECK (email=lower(btrim(email)) AND octet_length(email) BETWEEN 3 AND 254),
 company text NOT NULL DEFAULT '' CHECK (octet_length(company)<=200),
 stage text NOT NULL DEFAULT 'new' CHECK (stage IN ('new','contacted','qualified','demo','won','lost')),
 note text NOT NULL DEFAULT '' CHECK (octet_length(note)<=2000),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
 UNIQUE(owner_id,email)
);
CREATE INDEX sales_leads_owner_page ON sales_leads(owner_id,id);

CREATE TABLE sales_tasks (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 lead_id uuid NOT NULL REFERENCES sales_leads(id) ON DELETE CASCADE,
 title text NOT NULL CHECK (title=btrim(title) AND octet_length(title) BETWEEN 1 AND 200),
 due_on date NOT NULL CHECK (isfinite(due_on) AND due_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
 completed_at timestamptz CHECK (completed_at IS NULL OR isfinite(completed_at)),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
CREATE INDEX sales_tasks_lead ON sales_tasks(lead_id);
CREATE INDEX sales_tasks_due_page ON sales_tasks(due_on,id);


SET LOCAL lock_timeout = '5s';

-- Serialize deduplication and round-robin assignment across apid processes.
CREATE TABLE sales_intake_routing (
 id smallint PRIMARY KEY CHECK (id=1),
 last_owner_id uuid REFERENCES accounts(id) ON DELETE SET NULL
);
INSERT INTO sales_intake_routing(id) VALUES(1);

CREATE TABLE sales_inquiries (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 name text NOT NULL CHECK (name=btrim(name) AND octet_length(name) BETWEEN 1 AND 120),
 email text NOT NULL CHECK (email=lower(btrim(email)) AND octet_length(email) BETWEEN 3 AND 254),
 company text NOT NULL DEFAULT '' CHECK (octet_length(company)<=200),
 intent text NOT NULL CHECK (intent IN ('contact','demo')),
 message text NOT NULL CHECK (octet_length(message) BETWEEN 1 AND 1500),
 latest_message text NOT NULL DEFAULT '' CHECK (octet_length(latest_message)<=1500),
 requested_code text NOT NULL DEFAULT '' CHECK (requested_code='' OR requested_code ~ '^[A-Z0-9]{6}$'),
 status text NOT NULL CHECK (status IN ('pending','assigned','closed')),
 reason text NOT NULL CHECK (reason IN ('','no_reps','code_unavailable','owner_conflict','multiple_leads','owner_unavailable','lead_unavailable')),
 lead_id uuid REFERENCES sales_leads(id) ON DELETE SET NULL,
 task_id uuid REFERENCES sales_tasks(id) ON DELETE SET NULL,
 repeat_count bigint NOT NULL DEFAULT 0 CHECK (repeat_count>=0),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 last_received_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(last_received_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
CREATE UNIQUE INDEX sales_inquiries_active_email ON sales_inquiries(email) WHERE status<>'closed';
CREATE INDEX sales_inquiries_lead ON sales_inquiries(lead_id);
CREATE INDEX sales_inquiries_page ON sales_inquiries(status,id);

-- Opaque, expiring anti-replay receipts hold no contact or network information.
CREATE TABLE sales_intake_receipts (
 nonce uuid PRIMARY KEY CHECK (nonce <> '00000000-0000-0000-0000-000000000000'),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at))
);
CREATE INDEX sales_intake_receipts_expiry ON sales_intake_receipts(expires_at);

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE sales_activities (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 lead_id uuid NOT NULL REFERENCES sales_leads(id) ON DELETE CASCADE,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 kind text NOT NULL CHECK (kind IN ('call','email','demo','note')),
 summary text NOT NULL CHECK (summary=btrim(summary) AND octet_length(summary) BETWEEN 1 AND 1000),
 outcome text NOT NULL DEFAULT '' CHECK (octet_length(outcome)<=500),
 next_step text NOT NULL DEFAULT '' CHECK (octet_length(next_step)<=500),
 occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at) AND occurred_at>='2000-01-01T00:00:00Z'),
 recorded_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(recorded_at)),
 follow_up_title text NOT NULL DEFAULT '' CHECK (octet_length(follow_up_title)<=200),
 follow_up_on date CHECK (follow_up_on IS NULL OR (isfinite(follow_up_on) AND follow_up_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31')),
 task_id uuid REFERENCES sales_tasks(id) ON DELETE SET NULL,
 CHECK (occurred_at<=recorded_at),
 CHECK ((follow_up_title='')=(follow_up_on IS NULL))
);
CREATE INDEX sales_activities_page ON sales_activities(lead_id,occurred_at DESC,id DESC);
CREATE INDEX sales_activities_contact ON sales_activities(lead_id,occurred_at DESC) WHERE kind IN ('call','email','demo');
