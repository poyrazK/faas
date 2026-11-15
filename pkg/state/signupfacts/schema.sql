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

-- Projections of migration-owned tables the held-account linking reads
-- (migrations/20261114174509397_account_held_links.sql).
CREATE TABLE accounts (
 id uuid PRIMARY KEY,
 email text NOT NULL,
 abuse_hold_at timestamptz,
 abuse_hold_reason text
);
CREATE TABLE orgs (
 id uuid PRIMARY KEY,
 personal_org boolean NOT NULL,
 abuse_hold_at timestamptz,
 abuse_hold_reason text
);
CREATE TABLE org_memberships (
 org_id uuid NOT NULL,
 account_id uuid NOT NULL,
 role text NOT NULL,
 removed_at timestamptz
);
CREATE TABLE sessions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 issued_ip inet,
 issued_at timestamptz NOT NULL
);
CREATE TABLE account_held_links (
 account_id uuid NOT NULL,
 held_account_id uuid NOT NULL,
 held_org_id uuid,
 held_reason text NOT NULL,
 signal text NOT NULL,
 detected_at timestamptz NOT NULL,
 PRIMARY KEY (account_id, held_account_id, signal)
);
