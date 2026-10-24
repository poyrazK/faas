-- Offline projection of migration-owned tables for organization app admission.
CREATE TABLE accounts (id uuid PRIMARY KEY, status text NOT NULL, deletion_requested_at timestamptz, deletion_includes_managed_postgres boolean NOT NULL DEFAULT false);
CREATE TABLE orgs (
    id uuid PRIMARY KEY, plan text NOT NULL, status text NOT NULL,
    deleted_pending boolean NOT NULL, personal_org boolean NOT NULL,
    personal_owner_account_id uuid
);
CREATE TABLE apps (
    id uuid PRIMARY KEY, account_id uuid NOT NULL, org_id uuid,
    status text NOT NULL, preview_of_slug text, preview_pr_number integer
);
