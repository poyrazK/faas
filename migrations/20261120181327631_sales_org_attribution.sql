-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- An attribution's subject is a personal account or a company organization.
-- Both own a resource namespace, which invoices already reference.
ALTER TABLE referral_attributions ADD COLUMN IF NOT EXISTS subject_kind text NOT NULL DEFAULT 'account';
ALTER TABLE referral_attributions ADD COLUMN IF NOT EXISTS selected_by uuid;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='referral_attributions'::regclass AND conname='referral_attributions_subject_kind_check') THEN
  ALTER TABLE referral_attributions ADD CONSTRAINT referral_attributions_subject_kind_check
   CHECK (subject_kind IN ('account','org')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE referral_attributions VALIDATE CONSTRAINT referral_attributions_subject_kind_check;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='referral_attributions'::regclass AND conname='referral_attributions_namespace_fk') THEN
  ALTER TABLE referral_attributions ADD CONSTRAINT referral_attributions_namespace_fk
   FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE referral_attributions VALIDATE CONSTRAINT referral_attributions_namespace_fk;
ALTER TABLE referral_attributions DROP CONSTRAINT IF EXISTS referral_attributions_account_id_fkey;

-- Namespaces can outlive their account or organization, so deleting either
-- erases its incoming attribution exactly as the old account cascade did.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION erase_referral_subject() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM referral_attributions WHERE account_id=OLD.id;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER accounts_erase_referral_subject AFTER DELETE ON accounts
 FOR EACH ROW EXECUTE FUNCTION erase_referral_subject();
CREATE OR REPLACE TRIGGER orgs_erase_referral_subject AFTER DELETE ON orgs
 FOR EACH ROW EXECUTE FUNCTION erase_referral_subject();

-- New columns are appended so CREATE OR REPLACE keeps existing readers.
-- +goose StatementBegin
CREATE OR REPLACE VIEW sales_referral_overview AS
SELECT r.account_id,l.owner_id,l.kind,
 COALESCE(a.email,buyer.email,'') AS email,
 COALESCE(a.plan,o.plan,'')::text AS plan,
 COALESCE(a.status,o.status,'')::text AS account_status,
 r.created_at,r.stage,r.note,r.follow_up,r.currency,
 (SELECT count(*) FROM apps ap WHERE ap.account_id=r.account_id) AS apps,
 COALESCE(i.status='paid',false)::boolean AS paid,
 COALESCE(i.amount_paid_cents,0)::bigint AS paid_cents,
 (r.first_paid_at + make_interval(days=>r.hold_days))::timestamptz AS eligible_at,
 CASE
  WHEN r.first_paid_at IS NULL THEN 'registered'
  WHEN i.id IS NULL THEN 'review'
  WHEN i.status<>'paid' OR i.amount_refunded_cents>0 OR i.amount_refund_pending_cents>0 THEN 'reversed'
  WHEN i.currency<>r.currency THEN 'currency_mismatch'
  WHEN owner.status<>'active'
    OR (r.subject_kind='account' AND (a.id IS NULL OR a.status<>'active' OR a.email_verified_at IS NULL))
    OR (r.subject_kind='org' AND (o.id IS NULL OR o.status<>'active' OR o.deleted_pending)) THEN 'review'
  WHEN (l.kind='customer' AND r.customer_reward_cents=0 AND r.referred_reward_cents=0)
    OR (l.kind='sales' AND r.commission_bps=0) THEN 'unconfigured'
  WHEN now()<r.first_paid_at+make_interval(days=>r.hold_days) THEN 'holding'
  ELSE 'eligible'
 END AS reward_status,
 CASE WHEN l.kind='customer' THEN r.customer_reward_cents
  ELSE COALESCE(trunc(LEAST(i.subtotal_cents,GREATEST(i.amount_paid_cents-i.tax_cents,0))::numeric*r.commission_bps/10000)::bigint,0)
 END::bigint AS reward_cents,
 CASE WHEN l.kind='customer' THEN r.referred_reward_cents ELSE 0::bigint END AS referred_reward_cents,
 r.subject_kind,
 COALESCE(o.name,'')::text AS org_name,
 r.commission_bps,
 p.id AS payout_id,
 COALESCE(p.amount_cents,0)::bigint AS payout_cents,
 p.paid_on AS payout_paid_on
FROM referral_attributions r
JOIN referral_links l ON l.code=r.code
LEFT JOIN accounts a ON a.id=r.account_id AND r.subject_kind='account'
LEFT JOIN orgs o ON o.id=r.account_id AND r.subject_kind='org'
LEFT JOIN accounts buyer ON buyer.id=r.selected_by
JOIN accounts owner ON owner.id=l.owner_id
LEFT JOIN invoices i ON i.id=r.first_paid_invoice_id AND i.account_id=r.account_id
LEFT JOIN sales_commission_payouts p ON p.account_id=r.account_id AND p.voided_at IS NULL;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DROP VIEW IF EXISTS sales_referral_overview;
-- +goose StatementEnd
-- +goose StatementBegin
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
-- +goose StatementEnd
DROP TRIGGER IF EXISTS orgs_erase_referral_subject ON orgs;
DROP TRIGGER IF EXISTS accounts_erase_referral_subject ON accounts;
DROP FUNCTION IF EXISTS erase_referral_subject();
DELETE FROM referral_attributions WHERE subject_kind='org';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='referral_attributions'::regclass AND conname='referral_attributions_account_id_fkey') THEN
  ALTER TABLE referral_attributions ADD CONSTRAINT referral_attributions_account_id_fkey
   FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE referral_attributions DROP CONSTRAINT IF EXISTS referral_attributions_namespace_fk;
ALTER TABLE referral_attributions DROP CONSTRAINT IF EXISTS referral_attributions_subject_kind_check;
ALTER TABLE referral_attributions DROP COLUMN IF EXISTS selected_by;
ALTER TABLE referral_attributions DROP COLUMN IF EXISTS subject_kind;
RESET lock_timeout;
