-- Offline projection of the migration-owned account_signup_facts table
-- (migrations/20261113152925066_account_signup_facts.sql).
CREATE TABLE account_signup_facts (
 account_id uuid PRIMARY KEY,
 method text NOT NULL,
 signup_ip inet,
 user_agent text,
 country text,
 asn bigint,
 asn_org text,
 email_domain text NOT NULL,
 disposable_email boolean NOT NULL,
 created_at timestamptz NOT NULL,
 reduced_at timestamptz
);
