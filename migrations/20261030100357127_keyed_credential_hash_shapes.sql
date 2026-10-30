-- filename: 20261030100357127_keyed_credential_hash_shapes.sql
-- migration-compat: expand
-- ADR 20261029202403: credential hash columns hold either a plain SHA-256
-- (32 bytes, 64 hex characters) or a keyed digest (33 bytes: key id then
-- HMAC-SHA256; 66 hex characters). Each length or shape check is widened
-- online under a new name: added NOT VALID, validated, then the old one
-- dropped, each step committing on its own and guarded so a rerun
-- converges. Writers stay plain until apid's keyed writes are turned on, so
-- the previous release never meets a keyed value.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deploy_tokens'::regclass AND conname = 'deploy_tokens_hash_credhash_chk') THEN
  ALTER TABLE deploy_tokens ADD CONSTRAINT deploy_tokens_hash_credhash_chk CHECK ((octet_length(token_sha256) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE deploy_tokens VALIDATE CONSTRAINT deploy_tokens_hash_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE deploy_tokens DROP CONSTRAINT IF EXISTS deploy_tokens_hash_len_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_tenant_access_tokens'::regclass AND conname = 'platform_tenant_access_tokens_credhash_chk') THEN
  ALTER TABLE platform_tenant_access_tokens ADD CONSTRAINT platform_tenant_access_tokens_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_tenant_access_tokens VALIDATE CONSTRAINT platform_tenant_access_tokens_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_tenant_access_tokens DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'hosting_automation_credentials'::regclass AND conname = 'hosting_automation_credentials_credhash_chk') THEN
  ALTER TABLE hosting_automation_credentials ADD CONSTRAINT hosting_automation_credentials_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE hosting_automation_credentials VALIDATE CONSTRAINT hosting_automation_credentials_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE hosting_automation_credentials DROP CONSTRAINT IF EXISTS hosting_automation_credentials_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'issue_ingest_tokens'::regclass AND conname = 'issue_ingest_tokens_credhash_chk') THEN
  ALTER TABLE issue_ingest_tokens ADD CONSTRAINT issue_ingest_tokens_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_ingest_tokens VALIDATE CONSTRAINT issue_ingest_tokens_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_ingest_tokens DROP CONSTRAINT IF EXISTS issue_ingest_tokens_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'inbound_webhook_endpoints'::regclass AND conname = 'inbound_webhook_endpoints_credhash_chk') THEN
  ALTER TABLE inbound_webhook_endpoints ADD CONSTRAINT inbound_webhook_endpoints_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE inbound_webhook_endpoints VALIDATE CONSTRAINT inbound_webhook_endpoints_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE inbound_webhook_endpoints DROP CONSTRAINT IF EXISTS inbound_webhook_endpoints_token_hash_len_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'execution_artifact_grants'::regclass AND conname = 'execution_artifact_grants_credhash_chk') THEN
  ALTER TABLE execution_artifact_grants ADD CONSTRAINT execution_artifact_grants_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE execution_artifact_grants VALIDATE CONSTRAINT execution_artifact_grants_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE execution_artifact_grants DROP CONSTRAINT IF EXISTS execution_artifact_grants_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_publisher_credentials'::regclass AND conname = 'managed_realtime_publisher_credentials_credhash_chk') THEN
  ALTER TABLE managed_realtime_publisher_credentials ADD CONSTRAINT managed_realtime_publisher_credentials_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_publisher_credentials VALIDATE CONSTRAINT managed_realtime_publisher_credentials_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_publisher_credentials DROP CONSTRAINT IF EXISTS managed_realtime_publisher_credentials_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_worker_credentials'::regclass AND conname = 'managed_realtime_worker_credentials_credhash_chk') THEN
  ALTER TABLE managed_realtime_worker_credentials ADD CONSTRAINT managed_realtime_worker_credentials_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_worker_credentials VALIDATE CONSTRAINT managed_realtime_worker_credentials_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_worker_credentials DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_subscription_tickets'::regclass AND conname = 'managed_realtime_subscription_tickets_credhash_chk') THEN
  ALTER TABLE managed_realtime_subscription_tickets ADD CONSTRAINT managed_realtime_subscription_tickets_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_subscription_tickets VALIDATE CONSTRAINT managed_realtime_subscription_tickets_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_subscription_tickets DROP CONSTRAINT IF EXISTS managed_realtime_subscription_tickets_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'api_consumer_signups'::regclass AND conname = 'api_consumer_signups_claim_credhash_chk') THEN
  ALTER TABLE api_consumer_signups ADD CONSTRAINT api_consumer_signups_claim_credhash_chk CHECK ((octet_length(claim_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_consumer_signups VALIDATE CONSTRAINT api_consumer_signups_claim_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_consumer_signups DROP CONSTRAINT IF EXISTS api_consumer_signups_claim_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'magic_link_tokens'::regclass AND conname = 'magic_link_tokens_credhash_chk') THEN
  ALTER TABLE magic_link_tokens ADD CONSTRAINT magic_link_tokens_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE magic_link_tokens VALIDATE CONSTRAINT magic_link_tokens_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE magic_link_tokens DROP CONSTRAINT IF EXISTS magic_link_tokens_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'password_reset_tokens'::regclass AND conname = 'password_reset_tokens_credhash_chk') THEN
  ALTER TABLE password_reset_tokens ADD CONSTRAINT password_reset_tokens_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE password_reset_tokens VALIDATE CONSTRAINT password_reset_tokens_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE password_reset_tokens DROP CONSTRAINT IF EXISTS password_reset_tokens_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'mfa_disable_tokens'::regclass AND conname = 'mfa_disable_tokens_credhash_chk') THEN
  ALTER TABLE mfa_disable_tokens ADD CONSTRAINT mfa_disable_tokens_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE mfa_disable_tokens VALIDATE CONSTRAINT mfa_disable_tokens_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE mfa_disable_tokens DROP CONSTRAINT IF EXISTS mfa_disable_tokens_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'consumer_keys'::regclass AND conname = 'consumer_keys_hashed_secret_credhash_chk') THEN
  ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_hashed_secret_credhash_chk CHECK ((octet_length(hashed_secret) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE consumer_keys VALIDATE CONSTRAINT consumer_keys_hashed_secret_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_hashed_secret_len_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_forks'::regclass AND conname = 'app_forks_access_token_credhash_chk') THEN
  ALTER TABLE app_forks ADD CONSTRAINT app_forks_access_token_credhash_chk CHECK (((access_token_hash IS NULL) OR (octet_length(access_token_hash) = ANY (ARRAY[32, 33])))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_forks VALIDATE CONSTRAINT app_forks_access_token_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_forks DROP CONSTRAINT IF EXISTS app_forks_access_token_hash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'product_control_email_deliveries'::regclass AND conname = 'product_control_email_deliveries_unsubscribe_credhash_chk') THEN
  ALTER TABLE product_control_email_deliveries ADD CONSTRAINT product_control_email_deliveries_unsubscribe_credhash_chk CHECK (((unsubscribe_hash IS NULL) OR (octet_length(unsubscribe_hash) = ANY (ARRAY[32, 33])))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE product_control_email_deliveries VALIDATE CONSTRAINT product_control_email_deliveries_unsubscribe_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE product_control_email_deliveries DROP CONSTRAINT IF EXISTS product_control_email_deliveries_unsubscribe_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_verification_v2_check') THEN
  ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_v2_check CHECK ((((template = 'account_deletion_pending'::text) AND (deletion_requested_at IS NOT NULL) AND (restore_until IS NOT NULL) AND (verification_token_hash IS NULL) AND (octet_length(sealed_verification) = 0) AND (verification_key_id = ''::text) AND (verification_origin = ''::text)) OR ((template = ANY (ARRAY['mfa_recovery_used'::text, 'mfa_disabled'::text, 'password_changed'::text, 'mfa_enabled'::text, 'payment_failed'::text, 'account_restored'::text, 'account_suspended'::text, 'nonpayment_deletion'::text, 'quota_warning'::text, 'subscription_ended'::text, 'deployment_failed'::text, 'managed_postgres_cap'::text, 'agent_run_delivered'::text])) AND (deletion_requested_at IS NULL) AND (restore_until IS NULL) AND (verification_token_hash IS NULL) AND (octet_length(sealed_verification) = 0) AND (verification_key_id = ''::text) AND (verification_origin = ''::text)) OR ((template = ANY (ARRAY['email_verification'::text, 'password_reset'::text, 'magic_link'::text, 'mfa_disable_request'::text])) AND (deletion_requested_at IS NULL) AND (restore_until IS NULL) AND (((state = ANY (ARRAY['queued'::text, 'claimed'::text])) AND (verification_token_hash IS NOT NULL) AND (octet_length(verification_token_hash) = ANY (ARRAY[32, 33])) AND ((octet_length(sealed_verification) >= 64) AND (octet_length(sealed_verification) <= 4096)) AND (verification_key_id ~ '^[0-9a-f]{64}$'::text) AND ((length(verification_origin) >= 9) AND (length(verification_origin) <= 2048)) AND (verification_origin ~ '^https://[^/?#[:space:]]+$'::text)) OR ((state <> ALL (ARRAY['queued'::text, 'claimed'::text])) AND (verification_token_hash IS NULL) AND (octet_length(sealed_verification) = 0) AND (verification_key_id = ''::text) AND (verification_origin = ''::text)))))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_verification_v2_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'project_environment_approvals_plan_credhash_chk') THEN
  ALTER TABLE project_environment_approvals ADD CONSTRAINT project_environment_approvals_plan_credhash_chk CHECK ((plan_token_hash ~ '^[a-f0-9]{64}([a-f0-9]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals VALIDATE CONSTRAINT project_environment_approvals_plan_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals DROP CONSTRAINT IF EXISTS project_environment_approvals_plan_hash_shape;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'project_environment_approvals_approval_credhash_chk') THEN
  ALTER TABLE project_environment_approvals ADD CONSTRAINT project_environment_approvals_approval_credhash_chk CHECK ((approval_token_hash ~ '^[a-f0-9]{64}([a-f0-9]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals VALIDATE CONSTRAINT project_environment_approvals_approval_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals DROP CONSTRAINT IF EXISTS project_environment_approvals_token_hash_shape;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_queue_lease_receipts'::regclass AND conname = 'invocation_queue_lease_receipts_credhash_chk') THEN
  ALTER TABLE invocation_queue_lease_receipts ADD CONSTRAINT invocation_queue_lease_receipts_credhash_chk CHECK ((token_hash ~ '^[a-f0-9]{64}([a-f0-9]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_queue_lease_receipts VALIDATE CONSTRAINT invocation_queue_lease_receipts_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_queue_lease_receipts DROP CONSTRAINT IF EXISTS invocation_queue_lease_receipts_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_environment_queue_receipts'::regclass AND conname = 'invocation_environment_queue_receipts_credhash_chk') THEN
  ALTER TABLE invocation_environment_queue_receipts ADD CONSTRAINT invocation_environment_queue_receipts_credhash_chk CHECK ((token_hash ~ '^[a-f0-9]{64}([a-f0-9]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_environment_queue_receipts VALIDATE CONSTRAINT invocation_environment_queue_receipts_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_environment_queue_receipts DROP CONSTRAINT IF EXISTS invocation_environment_queue_receipts_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'workflow_automation_publish_receipts'::regclass AND conname = 'workflow_automation_publish_receipts_credhash_chk') THEN
  ALTER TABLE workflow_automation_publish_receipts ADD CONSTRAINT workflow_automation_publish_receipts_credhash_chk CHECK ((token_hash ~ '^[0-9a-f]{64}([0-9a-f]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workflow_automation_publish_receipts VALIDATE CONSTRAINT workflow_automation_publish_receipts_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workflow_automation_publish_receipts DROP CONSTRAINT IF EXISTS workflow_automation_publish_receipts_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'issue_slack_oauth_flows'::regclass AND conname = 'issue_slack_oauth_flows_state_credhash_chk') THEN
  ALTER TABLE issue_slack_oauth_flows ADD CONSTRAINT issue_slack_oauth_flows_state_credhash_chk CHECK ((state_hash ~ '^[0-9a-f]{64}([0-9a-f]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_slack_oauth_flows VALIDATE CONSTRAINT issue_slack_oauth_flows_state_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_slack_oauth_flows DROP CONSTRAINT IF EXISTS issue_slack_oauth_flows_state_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'issue_tracker_linear_oauth_flows'::regclass AND conname = 'issue_tracker_linear_oauth_flows_state_credhash_chk') THEN
  ALTER TABLE issue_tracker_linear_oauth_flows ADD CONSTRAINT issue_tracker_linear_oauth_flows_state_credhash_chk CHECK ((state_hash ~ '^[a-f0-9]{64}([a-f0-9]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_tracker_linear_oauth_flows VALIDATE CONSTRAINT issue_tracker_linear_oauth_flows_state_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_tracker_linear_oauth_flows DROP CONSTRAINT IF EXISTS issue_tracker_linear_oauth_flows_state_hash_check;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';
-- Rolling back needs every keyed value gone: the old checks fail to
-- validate while one remains.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deploy_tokens'::regclass AND conname = 'deploy_tokens_hash_len_chk') THEN
  ALTER TABLE deploy_tokens ADD CONSTRAINT deploy_tokens_hash_len_chk CHECK ((octet_length(token_sha256) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE deploy_tokens VALIDATE CONSTRAINT deploy_tokens_hash_len_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE deploy_tokens DROP CONSTRAINT IF EXISTS deploy_tokens_hash_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_tenant_access_tokens'::regclass AND conname = 'platform_tenant_access_tokens_token_hash_check') THEN
  ALTER TABLE platform_tenant_access_tokens ADD CONSTRAINT platform_tenant_access_tokens_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_tenant_access_tokens VALIDATE CONSTRAINT platform_tenant_access_tokens_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_tenant_access_tokens DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'hosting_automation_credentials'::regclass AND conname = 'hosting_automation_credentials_token_hash_check') THEN
  ALTER TABLE hosting_automation_credentials ADD CONSTRAINT hosting_automation_credentials_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE hosting_automation_credentials VALIDATE CONSTRAINT hosting_automation_credentials_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE hosting_automation_credentials DROP CONSTRAINT IF EXISTS hosting_automation_credentials_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'issue_ingest_tokens'::regclass AND conname = 'issue_ingest_tokens_token_hash_check') THEN
  ALTER TABLE issue_ingest_tokens ADD CONSTRAINT issue_ingest_tokens_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_ingest_tokens VALIDATE CONSTRAINT issue_ingest_tokens_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_ingest_tokens DROP CONSTRAINT IF EXISTS issue_ingest_tokens_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'inbound_webhook_endpoints'::regclass AND conname = 'inbound_webhook_endpoints_token_hash_len_chk') THEN
  ALTER TABLE inbound_webhook_endpoints ADD CONSTRAINT inbound_webhook_endpoints_token_hash_len_chk CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE inbound_webhook_endpoints VALIDATE CONSTRAINT inbound_webhook_endpoints_token_hash_len_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE inbound_webhook_endpoints DROP CONSTRAINT IF EXISTS inbound_webhook_endpoints_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'execution_artifact_grants'::regclass AND conname = 'execution_artifact_grants_token_hash_check') THEN
  ALTER TABLE execution_artifact_grants ADD CONSTRAINT execution_artifact_grants_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE execution_artifact_grants VALIDATE CONSTRAINT execution_artifact_grants_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE execution_artifact_grants DROP CONSTRAINT IF EXISTS execution_artifact_grants_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_publisher_credentials'::regclass AND conname = 'managed_realtime_publisher_credentials_token_hash_check') THEN
  ALTER TABLE managed_realtime_publisher_credentials ADD CONSTRAINT managed_realtime_publisher_credentials_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_publisher_credentials VALIDATE CONSTRAINT managed_realtime_publisher_credentials_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_publisher_credentials DROP CONSTRAINT IF EXISTS managed_realtime_publisher_credentials_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_worker_credentials'::regclass AND conname = 'managed_realtime_worker_credentials_token_hash_check') THEN
  ALTER TABLE managed_realtime_worker_credentials ADD CONSTRAINT managed_realtime_worker_credentials_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_worker_credentials VALIDATE CONSTRAINT managed_realtime_worker_credentials_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_worker_credentials DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_subscription_tickets'::regclass AND conname = 'managed_realtime_subscription_tickets_token_hash_check') THEN
  ALTER TABLE managed_realtime_subscription_tickets ADD CONSTRAINT managed_realtime_subscription_tickets_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_subscription_tickets VALIDATE CONSTRAINT managed_realtime_subscription_tickets_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_subscription_tickets DROP CONSTRAINT IF EXISTS managed_realtime_subscription_tickets_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'api_consumer_signups'::regclass AND conname = 'api_consumer_signups_claim_chk') THEN
  ALTER TABLE api_consumer_signups ADD CONSTRAINT api_consumer_signups_claim_chk CHECK ((octet_length(claim_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_consumer_signups VALIDATE CONSTRAINT api_consumer_signups_claim_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_consumer_signups DROP CONSTRAINT IF EXISTS api_consumer_signups_claim_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'magic_link_tokens'::regclass AND conname = 'magic_link_tokens_token_hash_check') THEN
  ALTER TABLE magic_link_tokens ADD CONSTRAINT magic_link_tokens_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE magic_link_tokens VALIDATE CONSTRAINT magic_link_tokens_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE magic_link_tokens DROP CONSTRAINT IF EXISTS magic_link_tokens_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'password_reset_tokens'::regclass AND conname = 'password_reset_tokens_token_hash_check') THEN
  ALTER TABLE password_reset_tokens ADD CONSTRAINT password_reset_tokens_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE password_reset_tokens VALIDATE CONSTRAINT password_reset_tokens_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE password_reset_tokens DROP CONSTRAINT IF EXISTS password_reset_tokens_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'mfa_disable_tokens'::regclass AND conname = 'mfa_disable_tokens_token_hash_check') THEN
  ALTER TABLE mfa_disable_tokens ADD CONSTRAINT mfa_disable_tokens_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE mfa_disable_tokens VALIDATE CONSTRAINT mfa_disable_tokens_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE mfa_disable_tokens DROP CONSTRAINT IF EXISTS mfa_disable_tokens_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'consumer_keys'::regclass AND conname = 'consumer_keys_hashed_secret_len_chk') THEN
  ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_hashed_secret_len_chk CHECK ((octet_length(hashed_secret) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE consumer_keys VALIDATE CONSTRAINT consumer_keys_hashed_secret_len_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_hashed_secret_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_forks'::regclass AND conname = 'app_forks_access_token_hash_chk') THEN
  ALTER TABLE app_forks ADD CONSTRAINT app_forks_access_token_hash_chk CHECK (((access_token_hash IS NULL) OR (octet_length(access_token_hash) = 32))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_forks VALIDATE CONSTRAINT app_forks_access_token_hash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_forks DROP CONSTRAINT IF EXISTS app_forks_access_token_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'product_control_email_deliveries'::regclass AND conname = 'product_control_email_deliveries_unsubscribe_hash_check') THEN
  ALTER TABLE product_control_email_deliveries ADD CONSTRAINT product_control_email_deliveries_unsubscribe_hash_check CHECK (((unsubscribe_hash IS NULL) OR (octet_length(unsubscribe_hash) = 32))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE product_control_email_deliveries VALIDATE CONSTRAINT product_control_email_deliveries_unsubscribe_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE product_control_email_deliveries DROP CONSTRAINT IF EXISTS product_control_email_deliveries_unsubscribe_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_verification_check') THEN
  ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK ((((template = 'account_deletion_pending'::text) AND (deletion_requested_at IS NOT NULL) AND (restore_until IS NOT NULL) AND (verification_token_hash IS NULL) AND (octet_length(sealed_verification) = 0) AND (verification_key_id = ''::text) AND (verification_origin = ''::text)) OR ((template = ANY (ARRAY['mfa_recovery_used'::text, 'mfa_disabled'::text, 'password_changed'::text, 'mfa_enabled'::text, 'payment_failed'::text, 'account_restored'::text, 'account_suspended'::text, 'nonpayment_deletion'::text, 'quota_warning'::text, 'subscription_ended'::text, 'deployment_failed'::text, 'managed_postgres_cap'::text, 'agent_run_delivered'::text])) AND (deletion_requested_at IS NULL) AND (restore_until IS NULL) AND (verification_token_hash IS NULL) AND (octet_length(sealed_verification) = 0) AND (verification_key_id = ''::text) AND (verification_origin = ''::text)) OR ((template = ANY (ARRAY['email_verification'::text, 'password_reset'::text, 'magic_link'::text, 'mfa_disable_request'::text])) AND (deletion_requested_at IS NULL) AND (restore_until IS NULL) AND (((state = ANY (ARRAY['queued'::text, 'claimed'::text])) AND (verification_token_hash IS NOT NULL) AND (octet_length(verification_token_hash) = 32) AND ((octet_length(sealed_verification) >= 64) AND (octet_length(sealed_verification) <= 4096)) AND (verification_key_id ~ '^[0-9a-f]{64}$'::text) AND ((length(verification_origin) >= 9) AND (length(verification_origin) <= 2048)) AND (verification_origin ~ '^https://[^/?#[:space:]]+$'::text)) OR ((state <> ALL (ARRAY['queued'::text, 'claimed'::text])) AND (verification_token_hash IS NULL) AND (octet_length(sealed_verification) = 0) AND (verification_key_id = ''::text) AND (verification_origin = ''::text)))))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_verification_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_v2_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'project_environment_approvals_plan_hash_shape') THEN
  ALTER TABLE project_environment_approvals ADD CONSTRAINT project_environment_approvals_plan_hash_shape CHECK ((plan_token_hash ~ '^[a-f0-9]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals VALIDATE CONSTRAINT project_environment_approvals_plan_hash_shape;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals DROP CONSTRAINT IF EXISTS project_environment_approvals_plan_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'project_environment_approvals_token_hash_shape') THEN
  ALTER TABLE project_environment_approvals ADD CONSTRAINT project_environment_approvals_token_hash_shape CHECK ((approval_token_hash ~ '^[a-f0-9]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals VALIDATE CONSTRAINT project_environment_approvals_token_hash_shape;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE project_environment_approvals DROP CONSTRAINT IF EXISTS project_environment_approvals_approval_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_queue_lease_receipts'::regclass AND conname = 'invocation_queue_lease_receipts_token_hash_check') THEN
  ALTER TABLE invocation_queue_lease_receipts ADD CONSTRAINT invocation_queue_lease_receipts_token_hash_check CHECK ((token_hash ~ '^[a-f0-9]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_queue_lease_receipts VALIDATE CONSTRAINT invocation_queue_lease_receipts_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_queue_lease_receipts DROP CONSTRAINT IF EXISTS invocation_queue_lease_receipts_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_environment_queue_receipts'::regclass AND conname = 'invocation_environment_queue_receipts_token_hash_check') THEN
  ALTER TABLE invocation_environment_queue_receipts ADD CONSTRAINT invocation_environment_queue_receipts_token_hash_check CHECK ((token_hash ~ '^[a-f0-9]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_environment_queue_receipts VALIDATE CONSTRAINT invocation_environment_queue_receipts_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE invocation_environment_queue_receipts DROP CONSTRAINT IF EXISTS invocation_environment_queue_receipts_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'workflow_automation_publish_receipts'::regclass AND conname = 'workflow_automation_publish_receipts_token_hash_check') THEN
  ALTER TABLE workflow_automation_publish_receipts ADD CONSTRAINT workflow_automation_publish_receipts_token_hash_check CHECK ((token_hash ~ '^[0-9a-f]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workflow_automation_publish_receipts VALIDATE CONSTRAINT workflow_automation_publish_receipts_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workflow_automation_publish_receipts DROP CONSTRAINT IF EXISTS workflow_automation_publish_receipts_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'issue_slack_oauth_flows'::regclass AND conname = 'issue_slack_oauth_flows_state_hash_check') THEN
  ALTER TABLE issue_slack_oauth_flows ADD CONSTRAINT issue_slack_oauth_flows_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_slack_oauth_flows VALIDATE CONSTRAINT issue_slack_oauth_flows_state_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_slack_oauth_flows DROP CONSTRAINT IF EXISTS issue_slack_oauth_flows_state_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'issue_tracker_linear_oauth_flows'::regclass AND conname = 'issue_tracker_linear_oauth_flows_state_hash_check') THEN
  ALTER TABLE issue_tracker_linear_oauth_flows ADD CONSTRAINT issue_tracker_linear_oauth_flows_state_hash_check CHECK ((state_hash ~ '^[a-f0-9]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_tracker_linear_oauth_flows VALIDATE CONSTRAINT issue_tracker_linear_oauth_flows_state_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE issue_tracker_linear_oauth_flows DROP CONSTRAINT IF EXISTS issue_tracker_linear_oauth_flows_state_credhash_chk;
-- +goose StatementEnd
