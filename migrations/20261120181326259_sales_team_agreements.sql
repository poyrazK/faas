-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Managers and salespeople managed by an owner admin in the sales portal.
-- Owner admins come only from FAAS_SALES_ADMIN_EMAILS. Removal deactivates.
CREATE TABLE IF NOT EXISTS sales_team_members (
 account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 role text NOT NULL CHECK (role IN ('manager','rep')),
 active boolean NOT NULL DEFAULT true,
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);

-- Append-only agreement history. The agreement in force when a customer is
-- attributed is copied onto that attribution and never rewritten.
CREATE TABLE IF NOT EXISTS sales_commission_agreements (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 commission_bps integer NOT NULL CHECK (commission_bps BETWEEN 0 AND 10000),
 effective_from timestamptz NOT NULL CHECK (isfinite(effective_from)),
 note text NOT NULL DEFAULT '' CHECK (octet_length(note) <= 500),
 created_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS sales_commission_agreements_current
 ON sales_commission_agreements(account_id,effective_from DESC,created_at DESC);

-- Audited moves of sales credit between salespeople.
CREATE TABLE IF NOT EXISTS referral_credit_changes (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES referral_attributions(account_id) ON DELETE CASCADE,
 from_owner_id uuid,
 to_owner_id uuid NOT NULL,
 from_commission_bps integer NOT NULL CHECK (from_commission_bps BETWEEN 0 AND 10000),
 to_commission_bps integer NOT NULL CHECK (to_commission_bps BETWEEN 0 AND 10000),
 reason text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 500),
 changed_by uuid,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS referral_credit_changes_account ON referral_credit_changes(account_id,created_at);

-- Bookkeeping of commission paid outside Gregale. No money moves here.
CREATE TABLE IF NOT EXISTS sales_commission_payouts (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES referral_attributions(account_id) ON DELETE CASCADE,
 owner_id uuid NOT NULL,
 amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000),
 currency text NOT NULL CHECK (currency IN ('usd','eur')),
 paid_on date NOT NULL CHECK (isfinite(paid_on)),
 reference text NOT NULL DEFAULT '' CHECK (octet_length(reference) <= 200),
 note text NOT NULL DEFAULT '' CHECK (octet_length(note) <= 500),
 recorded_by uuid,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 voided_at timestamptz CHECK (voided_at IS NULL OR isfinite(voided_at)),
 voided_by uuid,
 void_reason text NOT NULL DEFAULT '' CHECK (octet_length(void_reason) <= 500),
 CHECK ((voided_at IS NULL) = (void_reason = ''))
);
CREATE UNIQUE INDEX IF NOT EXISTS sales_commission_payouts_live
 ON sales_commission_payouts(account_id) WHERE voided_at IS NULL;
CREATE INDEX IF NOT EXISTS sales_commission_payouts_owner ON sales_commission_payouts(owner_id,paid_on);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS sales_commission_payouts;
DROP TABLE IF EXISTS referral_credit_changes;
DROP TABLE IF EXISTS sales_commission_agreements;
DROP TABLE IF EXISTS sales_team_members;
