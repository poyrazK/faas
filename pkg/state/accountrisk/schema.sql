-- Offline projection of the migration-owned tables the operator account risk
-- view reads (ADR-20261115090000). Columns not read here are omitted.
CREATE TABLE account_abuse_strikes (
 id bigint PRIMARY KEY,
 account_id uuid NOT NULL,
 reason text NOT NULL,
 app_id uuid,
 struck_at timestamptz NOT NULL
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
CREATE TABLE account_signup_facts (
 account_id uuid PRIMARY KEY,
 country text,
 email_domain text NOT NULL,
 disposable_email boolean NOT NULL
);
CREATE TABLE events (
 id bigint PRIMARY KEY,
 at timestamptz NOT NULL,
 actor text NOT NULL,
 kind text NOT NULL,
 subject uuid,
 data jsonb
);
CREATE TABLE apps (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL
);
CREATE TABLE deployments (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL
);
CREATE TABLE instances (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL
);
