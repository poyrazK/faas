-- +goose Up
SET LOCAL lock_timeout = '5s';
-- migration-lint: allow not-null managed_postgres_databases.org_id: bind_managed_postgres_resource_owner derives existing organization/restore/personal roots for legacy INSERTs after validated backfill.
-- migration-lint: allow not-null managed_postgres_bindings.database_account_id: The binding ownership trigger derives this namespace from database_id for legacy INSERTs; it is never guessed from the caller.
-- migration-lint: allow index-build managed_postgres_databases: Company name uniqueness must reject collisions before ownership commits; metadata writers are paused and production sizing/timing qualification is required (ADR-202610171906).
-- Replay completed expansion without replacing functions refined by later migrations.
-- A partially present expansion still fails atomically and requires repair.
-- +goose StatementBegin
DO $org_ownership_replay$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('managed_postgres_databases') AND attname='org_id' AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('managed_postgres_bindings') AND attname='database_account_id' AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_accounting_reconciliations') AND conname='managed_postgres_accounting_reconciliations_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_bindings') AND conname='managed_postgres_bindings_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_creation_receipts') AND conname='managed_postgres_creation_receipts_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_cutovers') AND conname='managed_postgres_cutovers_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_databases') AND conname='managed_postgres_databases_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_health') AND conname='managed_postgres_health_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_resizes') AND conname='managed_postgres_resizes_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_restore_verifications') AND conname='managed_postgres_restore_verifications_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_usage') AND conname='managed_postgres_usage_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_postgres_usage_imports') AND conname='managed_postgres_usage_imports_account_id_fkey')
    AND to_regclass('managed_postgres_databases_name_idx') IS NOT NULL
    AND to_regclass('managed_postgres_databases_org_idx') IS NOT NULL
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='bind_managed_postgres_resource_owner')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_managed_postgres_binding_owner')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_account_managed_postgres_databases')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='org_app_identity_erasure_blocked')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='org_owner_identity_active')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='org_streaming_plan_guard')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='pg_tier_rank')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='managed_postgres_usage_subject')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_org_managed_postgres_databases')
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_postgres_databases') AND tgname='managed_postgres_resource_owner' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_postgres_bindings') AND tgname='managed_postgres_binding_owner_guard' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('orgs') AND tgname='org_managed_postgres_databases_guard' AND NOT tgisinternal) THEN
  RETURN;
 END IF;

ALTER TABLE managed_postgres_databases ADD COLUMN org_id uuid REFERENCES orgs(id);
-- Binding and restore ancestry determine legacy ownership. Refuse mixed roots.
CREATE TEMP TABLE managed_postgres_owner_roots ON COMMIT DROP AS
WITH RECURSIVE edges AS (
 SELECT id AS child,restore_source_database_id AS parent FROM managed_postgres_databases WHERE restore_source_database_id IS NOT NULL
), seeds AS (
 SELECT b.database_id AS id,a.org_id FROM managed_postgres_bindings b JOIN apps a ON a.id=b.app_id
 UNION SELECT d.id,p.org_id FROM managed_postgres_databases d JOIN project_environment_clone_operations c ON c.id=d.environment_clone_operation_id JOIN projects p ON p.id=c.project_id
), roots(id,org_id) AS (
 SELECT id,org_id FROM seeds
 UNION
 SELECT CASE WHEN e.child=r.id THEN e.parent ELSE e.child END,r.org_id FROM roots r JOIN edges e ON e.child=r.id OR e.parent=r.id
)
SELECT id,min(org_id::text)::uuid AS org_id,count(DISTINCT org_id) AS owner_count FROM roots GROUP BY id;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_owner_roots WHERE owner_count<>1 OR org_id IS NULL) THEN
  RAISE EXCEPTION 'remediate mixed managed PostgreSQL ownership before migration' USING ERRCODE='23514';
 END IF;
END $$;
UPDATE managed_postgres_databases d SET org_id=coalesce(
 (SELECT r.org_id FROM managed_postgres_owner_roots r WHERE r.id=d.id),
 (SELECT o.id FROM orgs o WHERE o.personal_org AND o.personal_owner_account_id=d.account_id));
ALTER TABLE managed_postgres_databases ALTER COLUMN org_id SET NOT NULL;
DROP INDEX managed_postgres_databases_name_idx;
CREATE UNIQUE INDEX managed_postgres_databases_name_idx ON managed_postgres_databases(org_id,name) WHERE state<>'deleted';
CREATE INDEX managed_postgres_databases_org_idx ON managed_postgres_databases(org_id,id);
ALTER TABLE managed_postgres_bindings ADD COLUMN database_account_id uuid REFERENCES resource_namespaces(id);
UPDATE managed_postgres_bindings b SET database_account_id=d.account_id FROM managed_postgres_databases d WHERE d.id=b.database_id;
ALTER TABLE managed_postgres_bindings ALTER COLUMN database_account_id SET NOT NULL;
ALTER TABLE managed_postgres_accounting_reconciliations DROP CONSTRAINT managed_postgres_accounting_reconciliations_account_id_fkey;
ALTER TABLE managed_postgres_accounting_reconciliations ADD CONSTRAINT managed_postgres_accounting_reconciliations_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_account_id_fkey;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id);
ALTER TABLE managed_postgres_creation_receipts DROP CONSTRAINT managed_postgres_creation_receipts_account_id_fkey;
ALTER TABLE managed_postgres_creation_receipts ADD CONSTRAINT managed_postgres_creation_receipts_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE RESTRICT;
ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT managed_postgres_cutovers_account_id_fkey;
ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_databases_account_id_fkey;
ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id);
ALTER TABLE managed_postgres_health DROP CONSTRAINT managed_postgres_health_account_id_fkey;
ALTER TABLE managed_postgres_health ADD CONSTRAINT managed_postgres_health_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_resizes DROP CONSTRAINT managed_postgres_resizes_account_id_fkey;
ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_resizes_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_restore_verifications DROP CONSTRAINT managed_postgres_restore_verifications_account_id_fkey;
ALTER TABLE managed_postgres_restore_verifications ADD CONSTRAINT managed_postgres_restore_verifications_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE RESTRICT;
ALTER TABLE managed_postgres_usage DROP CONSTRAINT managed_postgres_usage_account_id_fkey;
ALTER TABLE managed_postgres_usage ADD CONSTRAINT managed_postgres_usage_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_usage_imports DROP CONSTRAINT managed_postgres_usage_imports_account_id_fkey;
ALTER TABLE managed_postgres_usage_imports ADD CONSTRAINT managed_postgres_usage_imports_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
CREATE FUNCTION bind_managed_postgres_resource_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.account_id<>OLD.account_id) THEN
  RAISE EXCEPTION 'database ownership is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resource_owner';
 END IF;
 IF NEW.org_id IS NULL AND NEW.environment_clone_operation_id IS NOT NULL THEN
  SELECT p.org_id INTO NEW.org_id FROM project_environment_clone_operations c JOIN projects p ON p.id=c.project_id WHERE c.id=NEW.environment_clone_operation_id;
 END IF;
 IF NEW.org_id IS NULL THEN SELECT id INTO NEW.org_id FROM orgs WHERE personal_org AND personal_owner_account_id=NEW.account_id; END IF;
 PERFORM id FROM orgs WHERE id=NEW.org_id AND NOT deleted_pending FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'database organization unavailable' USING ERRCODE='23514',CONSTRAINT='managed_postgres_resource_owner'; END IF;
 IF NOT EXISTS(SELECT 1 FROM orgs o JOIN resource_namespaces n ON n.id=NEW.account_id
               WHERE o.id=NEW.org_id AND (NOT o.personal_org OR o.personal_owner_account_id=NEW.account_id))
 OR (NEW.restore_source_database_id IS NOT NULL AND NOT EXISTS(
     SELECT 1 FROM managed_postgres_databases s WHERE s.id=NEW.restore_source_database_id AND s.org_id=NEW.org_id)) THEN
  RAISE EXCEPTION 'invalid database organization' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resource_owner';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER managed_postgres_resource_owner BEFORE INSERT OR UPDATE OF org_id,account_id,restore_source_database_id ON managed_postgres_databases
 FOR EACH ROW EXECUTE FUNCTION bind_managed_postgres_resource_owner();
CREATE OR REPLACE FUNCTION guard_managed_postgres_binding_owner() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.account_id<>OLD.account_id OR NEW.database_id<>OLD.database_id OR NEW.app_id<>OLD.app_id
       OR NEW.database_account_id IS DISTINCT FROM OLD.database_account_id) THEN
        RAISE EXCEPTION 'binding ownership is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_binding_owner';
    END IF;
    SELECT account_id INTO NEW.database_account_id FROM managed_postgres_databases WHERE id=NEW.database_id;
    IF NOT EXISTS (
        SELECT 1
        FROM managed_postgres_databases database
        JOIN apps app ON app.id = NEW.app_id
        WHERE database.id = NEW.database_id
          AND database.account_id = NEW.database_account_id
          AND database.org_id = app.org_id
          AND app.account_id = NEW.account_id
          AND database.state <> 'deleted'
          AND app.status <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'managed postgres binding owner mismatch or parent is deleted'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_binding_owner';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER managed_postgres_binding_owner_guard ON managed_postgres_bindings;
CREATE TRIGGER managed_postgres_binding_owner_guard BEFORE INSERT OR UPDATE OF account_id,database_id,app_id,database_account_id ON managed_postgres_bindings
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_binding_owner();
CREATE OR REPLACE FUNCTION guard_account_managed_postgres_databases() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.status = 'deleted_pending' AND EXISTS (
        SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
        WHERE d.account_id=NEW.id AND d.state<>'deleted'
          AND (o.personal_org OR NOT o.resource_ownership_enabled)
    ) THEN
        RAISE EXCEPTION 'account has managed postgres databases'
            USING ERRCODE = '23514', CONSTRAINT = 'account_has_managed_postgres_databases';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION org_app_identity_erasure_blocked(identity_id uuid) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $_$
DECLARE resource_table text; found boolean;
BEGIN
    IF EXISTS (SELECT 1 FROM org_memberships m JOIN orgs o ON o.id=m.org_id
               WHERE m.account_id=identity_id AND m.removed_at IS NULL
                 AND m.role='owner' AND NOT o.personal_org AND NOT o.deleted_pending) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
               WHERE d.account_id=identity_id AND NOT o.personal_org AND NOT o.resource_ownership_enabled) THEN
        RETURN true;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
                   WHERE d.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
                   WHERE a.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
                       WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN false;
    END IF;
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
               WHERE a.account_id=identity_id AND NOT o.personal_org
                 AND (a.github_install_id IS NOT NULL OR NOT o.resource_ownership_enabled)) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
               WHERE p.account_id=identity_id AND NOT o.personal_org
                 AND (coalesce(p.install_id,0) <> 0 OR NOT o.resource_ownership_enabled))
       OR EXISTS (SELECT 1 FROM environment_git_sources g JOIN projects p ON p.id=g.project_id
                  JOIN orgs o ON o.id=p.org_id WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN true;
    END IF;
    FOR resource_table IN SELECT unnest(ARRAY[
        'alert_deliveries',
        'alert_rules',
        'api_consumer_plan_admissions',
        'api_consumer_plan_assignments',
        'api_consumer_plans',
        'api_consumer_rate_cards',
        'api_consumer_route_usage_minutes',
        'api_consumer_usage_alerts',
        'api_consumer_usage_events',
        'api_consumer_usage_minutes',
        'api_consumer_usage_statement_handoffs',
        'api_consumer_usage_statements',
        'api_consumers',
        'app_api_routes',
        'app_issues',
        'app_private_network_attachment_nodes',
        'app_private_network_attachments',
        'app_route_priorities',
        'app_route_removal_policies',
        'app_tcp_listeners',
        'app_udp_listeners',
        'app_webhook_deliveries',
        'app_webhook_event_outbox',
        'app_webhooks',
        'application_standard_snapshot_captures',
        'automatic_route_checks',
        'canary_route_gates',
        'ci_automation_branches',
        'ci_automation_event_retries',
        'ci_automation_events',
        'ci_automation_path_checks',
        'ci_automation_policies',
        'ci_automation_pull_requests',
        'ci_execution_archives',
        'ci_execution_check_retries',
        'ci_execution_checks',
        'ci_execution_profiles',
        'ci_executions',
        'ci_github_bindings',
        'ci_github_checks',
        'ci_github_triggers',
        'ci_job_attempts',
        'ci_managed_objects',
        'ci_pipeline_revisions',
        'ci_run_events',
        'ci_runs',
        'ci_source_check_retries',
        'commit_sources',
        'consumer_keys',
        'cors_presets',
        'customer_operation_definitions',
        'customer_operation_idempotency',
        'customer_operation_stream_leases',
        'customer_operation_webhook_bindings',
        'customer_operation_workflow_states',
        'customer_operations',
        'customer_workflow_playbook_progress',
        'customer_workflow_readiness_subscriptions',
        'data_upstreams',
        'dead_letter_events',
        'dev_bridge_sessions',
        'dev_bridge_webhook_replays',
        'edge_rules',
        'entity_outbox_acceptances',
        'event_recovery_jobs',
        'event_subscription_capacity_commands',
        'event_subscription_circuit_breakers',
        'event_subscription_delivery_controls',
        'event_subscriptions',
        'exclusive_work_operations',
        'exclusive_work_trigger_bindings',
        'inbound_webhook_endpoints',
        'invocation_environment_queue_admissions',
        'issue_ingest_tokens',
        'job_runs',
        'managed_realtime_drain_operations',
        'managed_realtime_endpoints',
        'mcp_catalog_monitors',
        'meterd_tenant_surface_cert_expiry_state',
        'mirror_rules',
        'object_buckets',
        'object_s3_copy_source_grants',
        'object_storage_access_grants',
        'object_storage_multipart_uploads',
        'object_storage_s3_credentials',
        'object_storage_upload_grants',
        'object_upload_completions',
        'object_upload_routes',
        'object_version_protection',
        'outbound_app_bindings',
        'platform_tenant_usage_minutes',
        'prewarm_intents',
        'profile_canary_checks',
        'profile_deployment_checks',
        'profile_deployment_policies',
        'profile_investigations',
        'profile_periodic_monitors',
        'profile_route_alert_state',
        'project_environment_clone_checkpoint_postgres_credentials',
        'project_environment_clone_postgres_archives',
        'project_environment_clone_postgres_contents',
        'project_environment_clone_postgres_copy_readers',
        'project_environment_clone_postgres_copy_targets',
        'project_environment_clone_postgres_database_plans',
        'project_environment_clone_postgres_database_sql_pins',
        'project_environment_clone_postgres_imports',
        'project_environment_clone_postgres_inventories',
        'project_environment_clone_postgres_membership_plans',
        'project_environment_clone_postgres_role_plans',
        'project_environment_clone_postgres_snapshot_restores',
        'project_environment_clone_postgres_target_sql_pins',
        'project_environment_clone_postgres_verification_read_budgets',
        'project_environment_clone_postgres_verifications',
        'project_environment_queue_runtime_sets',
        'queue_bindings',
        'reserved_ip_leases',
        'route_check_history',
        'route_health_gates',
        'route_health_history',
        'route_health_notification_state',
        'route_lifecycle_approvals',
        'route_monitor_incidents',
        'route_monitors',
        'route_policy_receipts',
        'route_removal_approvals',
        'runtime_upgrade_operations',
        'saved_route_requirements',
        'product_control_billing_documents',
        'product_control_customer_changes',
        'product_control_customers',
        'product_control_email_subscriptions',
        'product_control_heads',
        'product_control_help_requests',
        'product_control_invoice_handoffs',
        'product_control_notification_reads',
        'product_control_onboardings',
        'product_control_plan_changes',
        'product_control_receipts',
        'product_control_revisions',
        'scenario_cold_preparations',
        'scenario_managed_work',
        'scenario_object_fixtures',
        'scenario_postgres_fixtures',
        'scenario_suspend_resumes',
        'scenario_test_action_policies',
        'scenario_test_chaos_matches',
        'scenario_test_members',
        'scenario_worker_faults',
        'schedule_occurrences',
        'tenant_surfaces',
        'triggers',
        'workflow_automation_publish_receipts',
        'workflow_operation_effects',
        'workflow_run_resumes'
    ]::text[]) LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $_$;


--
-- Name: org_owner_identity_active(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE OR REPLACE FUNCTION org_owner_identity_active() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE identity_status text;
BEGIN
 IF NEW.role='owner' AND NEW.removed_at IS NULL THEN
  SELECT status INTO identity_status FROM accounts WHERE id=NEW.account_id FOR KEY SHARE;
  IF identity_status IS DISTINCT FROM 'active' THEN
   RAISE EXCEPTION 'organization owner must have an active human identity' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: org_streaming_plan_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE OR REPLACE FUNCTION org_streaming_plan_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NOT NEW.personal_org AND NEW.plan='free' AND OLD.plan IS DISTINCT FROM NEW.plan
  AND EXISTS(SELECT 1 FROM apps WHERE org_id=NEW.id AND streaming_enabled) THEN
  RAISE EXCEPTION 'company streaming app prevents Free downgrade' USING ERRCODE='23514',CONSTRAINT='apps_streaming_enabled_plan_check';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: pg_tier_rank(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE OR REPLACE FUNCTION pg_tier_rank(tier text) RETURNS integer
    LANGUAGE sql IMMUTABLE
    AS $$
    SELECT CASE tier
        WHEN 'compose'    THEN 8
        WHEN 'k8s'        THEN 8
        WHEN 'render'     THEN 8
        WHEN 'fly'        THEN 8
        WHEN 'serverless' THEN 8
        WHEN 'procfile'   THEN 6
        WHEN 'workspace'  THEN 4
        WHEN 'convention' THEN 2
        WHEN 'single'     THEN 1
        ELSE 0
    END
$$;
CREATE FUNCTION managed_postgres_usage_subject(owner_org uuid, namespace uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE namespace END FROM orgs o WHERE o.id=owner_org
$$;
CREATE FUNCTION guard_org_managed_postgres_databases() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.deleted_pending OR NEW.status='deleted_pending') AND EXISTS(
     SELECT 1 FROM managed_postgres_databases WHERE org_id=NEW.id AND state<>'deleted') THEN
  RAISE EXCEPTION 'organization has managed PostgreSQL databases' USING ERRCODE='23514',CONSTRAINT='org_has_managed_postgres_databases';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER org_managed_postgres_databases_guard BEFORE UPDATE OF status,deleted_pending ON orgs
 FOR EACH ROW EXECUTE FUNCTION guard_org_managed_postgres_databases();
END;
$org_ownership_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TRIGGER org_managed_postgres_databases_guard ON orgs;
DROP FUNCTION guard_org_managed_postgres_databases();
DROP FUNCTION managed_postgres_usage_subject(uuid,uuid);
CREATE OR REPLACE FUNCTION org_app_identity_erasure_blocked(identity_id uuid) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $_$
DECLARE resource_table text; found boolean;
BEGIN
    IF EXISTS (SELECT 1 FROM org_memberships m JOIN orgs o ON o.id=m.org_id
               WHERE m.account_id=identity_id AND m.removed_at IS NULL
                 AND m.role='owner' AND NOT o.personal_org AND NOT o.deleted_pending) THEN
        RETURN true;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
                   WHERE a.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
                       WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN false;
    END IF;
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
               WHERE a.account_id=identity_id AND NOT o.personal_org
                 AND (a.github_install_id IS NOT NULL OR NOT o.resource_ownership_enabled)) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
               WHERE p.account_id=identity_id AND NOT o.personal_org
                 AND (coalesce(p.install_id,0) <> 0 OR NOT o.resource_ownership_enabled))
       OR EXISTS (SELECT 1 FROM environment_git_sources g JOIN projects p ON p.id=g.project_id
                  JOIN orgs o ON o.id=p.org_id WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN true;
    END IF;
    FOR resource_table IN SELECT unnest(ARRAY[
        'alert_deliveries',
        'alert_rules',
        'api_consumer_plan_admissions',
        'api_consumer_plan_assignments',
        'api_consumer_plans',
        'api_consumer_rate_cards',
        'api_consumer_route_usage_minutes',
        'api_consumer_usage_alerts',
        'api_consumer_usage_events',
        'api_consumer_usage_minutes',
        'api_consumer_usage_statement_handoffs',
        'api_consumer_usage_statements',
        'api_consumers',
        'app_api_routes',
        'app_issues',
        'app_private_network_attachment_nodes',
        'app_private_network_attachments',
        'app_route_priorities',
        'app_route_removal_policies',
        'app_tcp_listeners',
        'app_udp_listeners',
        'app_webhook_deliveries',
        'app_webhook_event_outbox',
        'app_webhooks',
        'application_standard_snapshot_captures',
        'automatic_route_checks',
        'canary_route_gates',
        'ci_automation_branches',
        'ci_automation_event_retries',
        'ci_automation_events',
        'ci_automation_path_checks',
        'ci_automation_policies',
        'ci_automation_pull_requests',
        'ci_execution_archives',
        'ci_execution_check_retries',
        'ci_execution_checks',
        'ci_execution_profiles',
        'ci_executions',
        'ci_github_bindings',
        'ci_github_checks',
        'ci_github_triggers',
        'ci_job_attempts',
        'ci_managed_objects',
        'ci_pipeline_revisions',
        'ci_run_events',
        'ci_runs',
        'ci_source_check_retries',
        'commit_sources',
        'consumer_keys',
        'cors_presets',
        'customer_operation_definitions',
        'customer_operation_idempotency',
        'customer_operation_stream_leases',
        'customer_operation_webhook_bindings',
        'customer_operation_workflow_states',
        'customer_operations',
        'customer_workflow_playbook_progress',
        'customer_workflow_readiness_subscriptions',
        'data_upstreams',
        'dead_letter_events',
        'dev_bridge_sessions',
        'dev_bridge_webhook_replays',
        'edge_rules',
        'entity_outbox_acceptances',
        'event_recovery_jobs',
        'event_subscription_capacity_commands',
        'event_subscription_circuit_breakers',
        'event_subscription_delivery_controls',
        'event_subscriptions',
        'exclusive_work_operations',
        'exclusive_work_trigger_bindings',
        'inbound_webhook_endpoints',
        'invocation_environment_queue_admissions',
        'issue_ingest_tokens',
        'job_runs',
        'managed_postgres_accounting_reconciliations',
        'managed_postgres_bindings',
        'managed_postgres_creation_receipts',
        'managed_postgres_cutovers',
        'managed_postgres_databases',
        'managed_postgres_health',
        'managed_postgres_resizes',
        'managed_postgres_restore_verifications',
        'managed_postgres_usage',
        'managed_postgres_usage_imports',
        'managed_realtime_drain_operations',
        'managed_realtime_endpoints',
        'mcp_catalog_monitors',
        'meterd_tenant_surface_cert_expiry_state',
        'mirror_rules',
        'object_buckets',
        'object_s3_copy_source_grants',
        'object_storage_access_grants',
        'object_storage_multipart_uploads',
        'object_storage_s3_credentials',
        'object_storage_upload_grants',
        'object_upload_completions',
        'object_upload_routes',
        'object_version_protection',
        'outbound_app_bindings',
        'platform_tenant_usage_minutes',
        'prewarm_intents',
        'profile_canary_checks',
        'profile_deployment_checks',
        'profile_deployment_policies',
        'profile_investigations',
        'profile_periodic_monitors',
        'profile_route_alert_state',
        'project_environment_clone_checkpoint_postgres_credentials',
        'project_environment_clone_postgres_archives',
        'project_environment_clone_postgres_contents',
        'project_environment_clone_postgres_copy_readers',
        'project_environment_clone_postgres_copy_targets',
        'project_environment_clone_postgres_database_plans',
        'project_environment_clone_postgres_database_sql_pins',
        'project_environment_clone_postgres_imports',
        'project_environment_clone_postgres_inventories',
        'project_environment_clone_postgres_membership_plans',
        'project_environment_clone_postgres_role_plans',
        'project_environment_clone_postgres_snapshot_restores',
        'project_environment_clone_postgres_target_sql_pins',
        'project_environment_clone_postgres_verification_read_budgets',
        'project_environment_clone_postgres_verifications',
        'project_environment_queue_runtime_sets',
        'queue_bindings',
        'reserved_ip_leases',
        'route_check_history',
        'route_health_gates',
        'route_health_history',
        'route_health_notification_state',
        'route_lifecycle_approvals',
        'route_monitor_incidents',
        'route_monitors',
        'route_policy_receipts',
        'route_removal_approvals',
        'runtime_upgrade_operations',
        'saved_route_requirements',
        'product_control_billing_documents',
        'product_control_customer_changes',
        'product_control_customers',
        'product_control_email_subscriptions',
        'product_control_heads',
        'product_control_help_requests',
        'product_control_invoice_handoffs',
        'product_control_notification_reads',
        'product_control_onboardings',
        'product_control_plan_changes',
        'product_control_receipts',
        'product_control_revisions',
        'scenario_cold_preparations',
        'scenario_managed_work',
        'scenario_object_fixtures',
        'scenario_postgres_fixtures',
        'scenario_suspend_resumes',
        'scenario_test_action_policies',
        'scenario_test_chaos_matches',
        'scenario_test_members',
        'scenario_worker_faults',
        'schedule_occurrences',
        'tenant_surfaces',
        'triggers',
        'workflow_automation_publish_receipts',
        'workflow_operation_effects',
        'workflow_run_resumes'
    ]::text[]) LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $_$;


--
-- Name: org_owner_identity_active(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE OR REPLACE FUNCTION org_owner_identity_active() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE identity_status text;
BEGIN
 IF NEW.role='owner' AND NEW.removed_at IS NULL THEN
  SELECT status INTO identity_status FROM accounts WHERE id=NEW.account_id FOR KEY SHARE;
  IF identity_status IS DISTINCT FROM 'active' THEN
   RAISE EXCEPTION 'organization owner must have an active human identity' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: org_streaming_plan_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE OR REPLACE FUNCTION org_streaming_plan_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NOT NEW.personal_org AND NEW.plan='free' AND OLD.plan IS DISTINCT FROM NEW.plan
  AND EXISTS(SELECT 1 FROM apps WHERE org_id=NEW.id AND streaming_enabled) THEN
  RAISE EXCEPTION 'company streaming app prevents Free downgrade' USING ERRCODE='23514',CONSTRAINT='apps_streaming_enabled_plan_check';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: pg_tier_rank(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE OR REPLACE FUNCTION pg_tier_rank(tier text) RETURNS integer
    LANGUAGE sql IMMUTABLE
    AS $$
    SELECT CASE tier
        WHEN 'compose'    THEN 8
        WHEN 'k8s'        THEN 8
        WHEN 'render'     THEN 8
        WHEN 'fly'        THEN 8
        WHEN 'serverless' THEN 8
        WHEN 'procfile'   THEN 6
        WHEN 'workspace'  THEN 4
        WHEN 'convention' THEN 2
        WHEN 'single'     THEN 1
        ELSE 0
    END
$$;
CREATE OR REPLACE FUNCTION guard_account_managed_postgres_databases() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.status = 'deleted_pending' AND EXISTS (
        SELECT 1 FROM managed_postgres_databases WHERE account_id = NEW.id AND state <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'account has managed postgres databases'
            USING ERRCODE = '23514', CONSTRAINT = 'account_has_managed_postgres_databases';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION guard_managed_postgres_binding_owner() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM managed_postgres_databases database
        JOIN apps app ON app.id = NEW.app_id
        WHERE database.id = NEW.database_id
          AND database.account_id = NEW.account_id
          AND app.account_id = NEW.account_id
          AND database.state <> 'deleted'
          AND app.status <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'managed postgres binding owner mismatch or parent is deleted'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_binding_owner';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER managed_postgres_resource_owner ON managed_postgres_databases;
DROP FUNCTION bind_managed_postgres_resource_owner();
DROP TRIGGER managed_postgres_binding_owner_guard ON managed_postgres_bindings;
CREATE TRIGGER managed_postgres_binding_owner_guard BEFORE INSERT OR UPDATE OF account_id,database_id,app_id ON managed_postgres_bindings
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_binding_owner();
ALTER TABLE managed_postgres_bindings DROP COLUMN database_account_id;
ALTER TABLE managed_postgres_usage_imports DROP CONSTRAINT managed_postgres_usage_imports_account_id_fkey;
ALTER TABLE managed_postgres_usage_imports ADD CONSTRAINT managed_postgres_usage_imports_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_usage DROP CONSTRAINT managed_postgres_usage_account_id_fkey;
ALTER TABLE managed_postgres_usage ADD CONSTRAINT managed_postgres_usage_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_restore_verifications DROP CONSTRAINT managed_postgres_restore_verifications_account_id_fkey;
ALTER TABLE managed_postgres_restore_verifications ADD CONSTRAINT managed_postgres_restore_verifications_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE RESTRICT;
ALTER TABLE managed_postgres_resizes DROP CONSTRAINT managed_postgres_resizes_account_id_fkey;
ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_resizes_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_health DROP CONSTRAINT managed_postgres_health_account_id_fkey;
ALTER TABLE managed_postgres_health ADD CONSTRAINT managed_postgres_health_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_databases_account_id_fkey;
ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id);
ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT managed_postgres_cutovers_account_id_fkey;
ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE managed_postgres_creation_receipts DROP CONSTRAINT managed_postgres_creation_receipts_account_id_fkey;
ALTER TABLE managed_postgres_creation_receipts ADD CONSTRAINT managed_postgres_creation_receipts_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE RESTRICT;
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_account_id_fkey;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id);
ALTER TABLE managed_postgres_accounting_reconciliations DROP CONSTRAINT managed_postgres_accounting_reconciliations_account_id_fkey;
ALTER TABLE managed_postgres_accounting_reconciliations ADD CONSTRAINT managed_postgres_accounting_reconciliations_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
DROP INDEX managed_postgres_databases_org_idx;
DROP INDEX managed_postgres_databases_name_idx;
CREATE UNIQUE INDEX managed_postgres_databases_name_idx ON managed_postgres_databases(account_id,name) WHERE state<>'deleted';
ALTER TABLE managed_postgres_databases DROP COLUMN org_id;
-- +goose StatementEnd
