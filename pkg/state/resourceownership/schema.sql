-- Offline projection of the migration-owned resource policy and billing fields.
CREATE TABLE orgs (
 id uuid PRIMARY KEY, resource_ownership_enabled boolean NOT NULL, plan text NOT NULL, status text NOT NULL, personal_org boolean NOT NULL,
 deleted_pending boolean NOT NULL, provider_customer_id text, stripe_subscription_item text,
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, past_due_at timestamptz, last_quota_warning_at timestamptz,
 suspended_reason text, abuse_hold_at timestamptz, abuse_hold_reason text,
 billing_email text NOT NULL, business_name text NOT NULL, billing_address text NOT NULL, tax_id text NOT NULL
);
CREATE TABLE managed_postgres_databases(id uuid PRIMARY KEY,account_id uuid NOT NULL,org_id uuid NOT NULL);
CREATE TABLE resource_namespaces (id uuid PRIMARY KEY);
CREATE TABLE billing_identities (account_id uuid NOT NULL, provider text NOT NULL, customer_id text NOT NULL, subscription_id text NOT NULL, PRIMARY KEY(account_id,provider));
CREATE TABLE apps (id uuid PRIMARY KEY,account_id uuid NOT NULL,org_id uuid,streaming_enabled boolean NOT NULL);
CREATE TABLE usage_minutes (instance_id uuid NOT NULL,minute timestamptz NOT NULL,account_id uuid NOT NULL,app_id uuid);
CREATE TABLE app_secrets(account_id uuid NOT NULL,app_id uuid NOT NULL);
CREATE TABLE app_trusted_signers(account_id uuid NOT NULL,app_id uuid NOT NULL);
CREATE TABLE app_registry_credentials(account_id uuid NOT NULL,app_id uuid NOT NULL);
CREATE TABLE customer_operations(account_id uuid NOT NULL,app_id uuid NOT NULL);
CREATE TABLE invocations(account_id uuid NOT NULL,app_id uuid NOT NULL);
CREATE TABLE usage_daily(account_id uuid NOT NULL,app_id uuid NOT NULL,org_id uuid);
CREATE TABLE builder_usage(account_id uuid NOT NULL,app_id uuid NOT NULL,org_id uuid);
CREATE TABLE snapshot_storage_daily(account_id uuid NOT NULL,app_id uuid NOT NULL);
CREATE TABLE projects (
 id uuid PRIMARY KEY, account_id uuid NOT NULL, org_id uuid NOT NULL, slug text NOT NULL,
 repo_full_name text, production_branch text, install_id bigint, scan_source text NOT NULL,
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

-- ADR-202610171907: retained object namespaces block namespace pruning.
CREATE TABLE object_buckets (id uuid PRIMARY KEY, account_id uuid NOT NULL);
