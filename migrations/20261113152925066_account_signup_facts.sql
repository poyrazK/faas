-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113160000: what an account looked like when it signed up, for
-- abuse review and for linking a new account to a held one. The address and
-- user agent are personal data kept for 90 days (meterd reduces the row:
-- reduced_at is set and both are cleared); the method, country, network owner
-- and email domain stay for the life of the account. Deleted with the account.
CREATE TABLE IF NOT EXISTS account_signup_facts (
 account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 method text NOT NULL CHECK (method ~ '^[a-z][a-z0-9_]{0,31}$'),
 signup_ip inet,
 user_agent text CHECK (char_length(user_agent) <= 512),
 country text CHECK (country ~ '^[A-Z]{2}$'),
 asn bigint CHECK (asn > 0 AND asn <= 4294967295),
 asn_org text CHECK (char_length(asn_org) <= 200),
 email_domain text NOT NULL CHECK (char_length(email_domain) <= 253),
 disposable_email boolean NOT NULL,
 created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
 reduced_at timestamptz CHECK (isfinite(reduced_at)),
 CHECK (reduced_at IS NULL OR (signup_ip IS NULL AND user_agent IS NULL))
);
-- Linking a new account to held ones looks up recent sign-ups by network.
CREATE INDEX IF NOT EXISTS account_signup_facts_ip_idx ON account_signup_facts USING gist (signup_ip inet_ops) WHERE signup_ip IS NOT NULL;
CREATE INDEX IF NOT EXISTS account_signup_facts_unreduced_idx ON account_signup_facts (created_at) WHERE reduced_at IS NULL;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS account_signup_facts;
