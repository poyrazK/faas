-- Offline projection of the migration-owned support grant tables
-- (migrations/20261116162429550_support_grants.sql) and the columns their
-- queries read from orgs, org_memberships and accounts.
CREATE TABLE orgs (
 id uuid PRIMARY KEY,
 slug text NOT NULL,
 personal_org boolean NOT NULL,
 personal_owner_account_id uuid
);
CREATE TABLE accounts (
 id uuid PRIMARY KEY,
 status text NOT NULL
);
CREATE TABLE org_memberships (
 org_id uuid NOT NULL,
 account_id uuid NOT NULL,
 role text NOT NULL,
 removed_at timestamptz,
 PRIMARY KEY (org_id, account_id)
);
CREATE TABLE support_grant_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 org_id uuid NOT NULL,
 operator_account_id uuid NOT NULL,
 reason text NOT NULL,
 status text NOT NULL DEFAULT 'open',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 decided_at timestamptz,
 decided_by_account_id uuid
);
CREATE TABLE support_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 org_id uuid NOT NULL,
 app_ids uuid[],
 created_by_account_id uuid NOT NULL,
 request_id uuid,
 reason text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 revoked_by_account_id uuid
);
