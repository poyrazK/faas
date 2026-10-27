-- Compile-time projection of the tables the agent queries read. Runtime
-- schema is owned by migrations 20261027141003390, 20261027141010563,
-- 20261027141017736 and 20261027141024909 (and, for accounts,
-- platform_tenants, outbound_integrations, github_installations and the
-- ADR-076 webhook ledger, earlier migrations). This file never executes against a database.
CREATE TABLE accounts (
 id uuid NOT NULL,
 plan text NOT NULL
);
CREATE TABLE platform_tenants (
 id uuid NOT NULL,
 account_id uuid NOT NULL,
 status text NOT NULL
);
CREATE TABLE app_webhooks (
 id uuid NOT NULL,
 account_id uuid NOT NULL,
 scope text NOT NULL DEFAULT 'app',
 enabled boolean NOT NULL DEFAULT true,
 event_filter text[] NOT NULL DEFAULT '{}'
);
CREATE TABLE app_webhook_deliveries (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 webhook_id uuid NOT NULL,
 app_id uuid,
 account_id uuid NOT NULL,
 event text NOT NULL,
 payload jsonb NOT NULL,
 source_event_id uuid
);
CREATE TABLE github_installations (
 account_id uuid NOT NULL,
 installation_id bigint NOT NULL,
 sealed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE outbound_integrations (
 id uuid NOT NULL,
 account_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 owner_kind text NOT NULL DEFAULT 'operator',
 ai_provider text
);
CREATE TABLE agents (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL,
 name text NOT NULL,
 description text NOT NULL DEFAULT '',
 state text NOT NULL DEFAULT 'active',
 latest_version integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz
);
CREATE TABLE agent_versions (
 agent_id uuid NOT NULL,
 version integer NOT NULL,
 account_id uuid NOT NULL,
 harness_preset text,
 harness_image text,
 harness_run text[] NOT NULL DEFAULT '{}',
 harness_models jsonb NOT NULL DEFAULT '{}',
 prompt_template text NOT NULL,
 ram_mb integer,
 timeout_sec integer NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 model_integrations jsonb NOT NULL DEFAULT '{}',
 ai_budget_millicents bigint,
 deliveries jsonb NOT NULL DEFAULT '[]',
 PRIMARY KEY (agent_id, version)
);
CREATE TABLE agent_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 agent_version integer NOT NULL,
 tenant_id uuid,
 trigger text NOT NULL,
 dedupe_key text,
 prompt text,
 inputs jsonb NOT NULL DEFAULT '{}',
 state text NOT NULL DEFAULT 'queued',
 ram_mb integer,
 timeout_sec integer NOT NULL,
 attempt integer NOT NULL DEFAULT 1,
 lease_token text,
 lease_expires_at timestamptz,
 cancel_requested_at timestamptz,
 outcome_code text,
 outcome_message text,
 queued_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 running_at timestamptz,
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 lease_owner text,
 node_id text,
 instance_id text,
 exit_code integer,
 signal integer,
 boot_ms integer,
 ai_budget_millicents bigint,
 checkout_repository text,
 checkout_commit text,
 checkout_installation_id bigint,
 github_repository text,
 github_number bigint,
 github_installation_id bigint,
 workflow_run_id uuid,
 workflow_step text,
 workflow_event text
);
CREATE TABLE agent_run_outputs (
 run_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 result jsonb,
 files jsonb NOT NULL DEFAULT '[]',
 log_tail text NOT NULL DEFAULT '',
 log_truncated boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE agent_deliveries (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 ordinal smallint NOT NULL,
 destination text NOT NULL,
 idempotency_key text NOT NULL,
 installation_id bigint,
 repository text,
 number bigint,
 body text NOT NULL DEFAULT '',
 body_truncated boolean NOT NULL DEFAULT false,
 state text NOT NULL DEFAULT 'pending',
 attempts integer NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 deadline_at timestamptz NOT NULL,
 lease_token text,
 lease_expires_at timestamptz,
 outcome_code text,
 outcome_message text,
 external_id text,
 external_url text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 bucket_id uuid,
 bucket_app_id uuid,
 object_prefix text
);
CREATE TABLE object_buckets (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 state text NOT NULL
);
CREATE TABLE platform_mail_agent_run_observations (
 delivery_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 disposition text NOT NULL
);
CREATE TABLE workflow_runs (
 id uuid PRIMARY KEY,
 status text NOT NULL
);
CREATE TABLE workflow_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 run_id uuid NOT NULL,
 event_name text NOT NULL
);
