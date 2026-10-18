-- +goose Up
SET LOCAL lock_timeout = '5s';
-- migration-lint: allow not-null projects.org_id: bind_project_resource_owner derives the organization from the existing personal/project root for legacy INSERTs; backfill is validated before tightening.
-- migration-lint: allow index-build projects: The unique company slug index must reject ownership collisions inside the atomic cutover; metadata writers are paused and production sizing/timing qualification is required (ADR-202610171905).
-- Replay completed expansion without replacing functions refined by later migrations.
-- A partially present expansion still fails atomically and requires repair.
-- +goose StatementBegin
DO $org_ownership_replay$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('account_deploy_rate_limits') AND conname='account_deploy_rate_limits_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('app_service_address_cursors') AND conname='app_service_address_cursors_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('idempotency_keys') AND conname='idempotency_keys_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('app_environment_route_budgets') AND conname='app_environment_route_budgets_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('app_environment_secret_ref_suppressions') AND conname='app_environment_secret_ref_suppressions_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('app_environment_secret_refs') AND conname='app_environment_secret_refs_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('app_environment_workload_intents') AND conname='app_environment_workload_intents_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('app_work_policies') AND conname='app_work_policies_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('environment_route_budget_receipts') AND conname='environment_route_budget_receipts_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('environment_workload_retirements') AND conname='environment_workload_retirements_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('feature_flag_versions') AND conname='feature_flag_versions_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_approvals') AND conname='project_environment_approvals_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_clone_configuration_guards') AND conname='project_environment_clone_configuration_guards_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_clone_operations') AND conname='project_environment_clone_operations_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_config_versions') AND conname='project_environment_config_versions_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_edge_policies') AND conname='project_environment_edge_policies_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_promotions') AND conname='project_environment_promotions_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_qualifications') AND conname='project_environment_qualifications_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_route_policies') AND conname='project_environment_route_policies_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environments') AND conname='project_environments_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_release_sets') AND conname='project_release_sets_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('projects') AND conname='projects_account_id_fkey')
    AND to_regclass('projects_org_slug_uniq') IS NOT NULL
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='bind_project_resource_owner')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='bind_project_workload_owner')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='org_app_identity_erasure_blocked')
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('projects') AND tgname='project_resource_owner' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('apps') AND tgname='aa_project_workload_owner' AND NOT tgisinternal) THEN
  RETURN;
 END IF;

ALTER TABLE account_deploy_rate_limits DROP CONSTRAINT account_deploy_rate_limits_account_id_fkey;
ALTER TABLE account_deploy_rate_limits ADD CONSTRAINT account_deploy_rate_limits_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE app_service_address_cursors DROP CONSTRAINT app_service_address_cursors_account_id_fkey;
ALTER TABLE app_service_address_cursors ADD CONSTRAINT app_service_address_cursors_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_account_id_fkey;
ALTER TABLE idempotency_keys ADD CONSTRAINT idempotency_keys_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id);
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM apps WHERE project_id IS NOT NULL GROUP BY project_id HAVING count(DISTINCT org_id)>1)
 OR EXISTS (SELECT 1 FROM apps a JOIN projects p ON p.id=a.project_id WHERE a.account_id<>p.account_id)
 OR EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id JOIN apps a ON a.project_id=p.id
            WHERE NOT o.personal_org AND a.org_id IS DISTINCT FROM p.org_id) THEN
  RAISE EXCEPTION 'remediate mixed project ownership before organization migration' USING ERRCODE='23514';
 END IF;
END $$;
UPDATE projects p SET org_id=coalesce(
 (SELECT a.org_id FROM apps a WHERE a.project_id=p.id ORDER BY a.created_at,a.id LIMIT 1), p.org_id,
 (SELECT o.id FROM orgs o WHERE o.personal_org AND o.personal_owner_account_id=p.account_id));
ALTER TABLE projects ALTER COLUMN org_id SET NOT NULL;
CREATE UNIQUE INDEX projects_org_slug_uniq ON projects(org_id,slug);
ALTER TABLE app_environment_route_budgets DROP CONSTRAINT app_environment_route_budgets_account_id_fkey;
ALTER TABLE app_environment_route_budgets ADD CONSTRAINT app_environment_route_budgets_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT app_environment_secret_ref_suppressions_account_id_fkey;
ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT app_environment_secret_ref_suppressions_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT app_environment_secret_refs_account_id_fkey;
ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE app_environment_workload_intents DROP CONSTRAINT app_environment_workload_intents_account_id_fkey;
ALTER TABLE app_environment_workload_intents ADD CONSTRAINT app_environment_workload_intents_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE app_work_policies DROP CONSTRAINT app_work_policies_account_id_fkey;
ALTER TABLE app_work_policies ADD CONSTRAINT app_work_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE environment_route_budget_receipts DROP CONSTRAINT environment_route_budget_receipts_account_id_fkey;
ALTER TABLE environment_route_budget_receipts ADD CONSTRAINT environment_route_budget_receipts_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE environment_workload_retirements DROP CONSTRAINT environment_workload_retirements_account_id_fkey;
ALTER TABLE environment_workload_retirements ADD CONSTRAINT environment_workload_retirements_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE feature_flag_versions DROP CONSTRAINT feature_flag_versions_account_id_fkey;
ALTER TABLE feature_flag_versions ADD CONSTRAINT feature_flag_versions_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_approvals DROP CONSTRAINT project_environment_approvals_account_id_fkey;
ALTER TABLE project_environment_approvals ADD CONSTRAINT project_environment_approvals_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_clone_configuration_guards DROP CONSTRAINT project_environment_clone_configuration_guards_account_id_fkey;
ALTER TABLE project_environment_clone_configuration_guards ADD CONSTRAINT project_environment_clone_configuration_guards_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_clone_operations DROP CONSTRAINT project_environment_clone_operations_account_id_fkey;
ALTER TABLE project_environment_clone_operations ADD CONSTRAINT project_environment_clone_operations_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_config_versions DROP CONSTRAINT project_environment_config_versions_account_id_fkey;
ALTER TABLE project_environment_config_versions ADD CONSTRAINT project_environment_config_versions_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_edge_policies DROP CONSTRAINT project_environment_edge_policies_account_id_fkey;
ALTER TABLE project_environment_edge_policies ADD CONSTRAINT project_environment_edge_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_promotions DROP CONSTRAINT project_environment_promotions_account_id_fkey;
ALTER TABLE project_environment_promotions ADD CONSTRAINT project_environment_promotions_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_qualifications DROP CONSTRAINT project_environment_qualifications_account_id_fkey;
ALTER TABLE project_environment_qualifications ADD CONSTRAINT project_environment_qualifications_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_route_policies DROP CONSTRAINT project_environment_route_policies_account_id_fkey;
ALTER TABLE project_environment_route_policies ADD CONSTRAINT project_environment_route_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environments DROP CONSTRAINT project_environments_account_id_fkey;
ALTER TABLE project_environments ADD CONSTRAINT project_environments_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_release_sets DROP CONSTRAINT project_release_sets_account_id_fkey;
ALTER TABLE project_release_sets ADD CONSTRAINT project_release_sets_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE projects DROP CONSTRAINT projects_account_id_fkey;
ALTER TABLE projects ADD CONSTRAINT projects_account_id_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;

CREATE FUNCTION bind_project_resource_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.account_id<>OLD.account_id) THEN
  RAISE EXCEPTION 'project ownership is immutable' USING ERRCODE='23514', CONSTRAINT='project_resource_owner';
 END IF;
 IF NEW.org_id IS NULL THEN
  SELECT o.id INTO NEW.org_id FROM orgs o WHERE o.personal_org AND o.personal_owner_account_id=NEW.account_id;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM orgs o JOIN resource_namespaces n ON n.id=NEW.account_id
                WHERE o.id=NEW.org_id AND (NOT o.personal_org OR o.personal_owner_account_id=NEW.account_id)) THEN
  RAISE EXCEPTION 'invalid project ownership' USING ERRCODE='23503', CONSTRAINT='project_resource_owner';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER project_resource_owner BEFORE INSERT OR UPDATE ON projects
 FOR EACH ROW EXECUTE FUNCTION bind_project_resource_owner();

CREATE FUNCTION bind_project_workload_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p projects%ROWTYPE;
BEGIN
 IF NEW.project_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO p FROM projects WHERE id=NEW.project_id FOR SHARE;
 IF NOT FOUND OR p.account_id<>NEW.account_id THEN
  RAISE EXCEPTION 'workload project namespace mismatch' USING ERRCODE='23514', CONSTRAINT='project_workload_owner';
 END IF;
 IF NEW.org_id IS NULL THEN NEW.org_id=p.org_id; END IF;
 IF p.org_id<>NEW.org_id THEN
  RAISE EXCEPTION 'workload project organization mismatch' USING ERRCODE='23514', CONSTRAINT='project_workload_owner';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER aa_project_workload_owner BEFORE INSERT OR UPDATE OF project_id,account_id,org_id ON apps
 FOR EACH ROW EXECUTE FUNCTION bind_project_workload_owner();
CREATE OR REPLACE FUNCTION org_app_identity_erasure_blocked(identity_id uuid) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
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
        'mcp_readiness_attention_actions',
        'mcp_readiness_incidents',
        'mcp_readiness_maintenance_windows',
        'mcp_readiness_notification_actions',
        'app_slos',
        'notification_channels',
        'synthetic_checks',
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
        'managed_postgres_connection_capacity',
        'data_api_runtime_connection_observations',
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
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $$;
END;
$org_ownership_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE account_deploy_rate_limits DROP CONSTRAINT account_deploy_rate_limits_account_id_fkey;
ALTER TABLE account_deploy_rate_limits ADD CONSTRAINT account_deploy_rate_limits_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_service_address_cursors DROP CONSTRAINT app_service_address_cursors_account_id_fkey;
ALTER TABLE app_service_address_cursors ADD CONSTRAINT app_service_address_cursors_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_account_id_fkey;
ALTER TABLE idempotency_keys ADD CONSTRAINT idempotency_keys_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id);
CREATE OR REPLACE FUNCTION org_app_identity_erasure_blocked(identity_id uuid) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
DECLARE resource_table text; found boolean;
BEGIN
    IF EXISTS (SELECT 1 FROM org_memberships m JOIN orgs o ON o.id=m.org_id
               WHERE m.account_id=identity_id AND m.removed_at IS NULL
                 AND m.role='owner' AND NOT o.personal_org AND NOT o.deleted_pending) THEN
        RETURN true;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
                   WHERE a.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN false;
    END IF;
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
               WHERE a.account_id=identity_id AND NOT o.personal_org
                 AND (a.project_id IS NOT NULL OR a.github_install_id IS NOT NULL OR NOT o.resource_ownership_enabled)) THEN
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
        'app_environment_route_budgets',
        'app_environment_secret_ref_suppressions',
        'app_environment_secret_refs',
        'app_environment_workload_intents',
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
        'app_work_policies',
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
        'environment_route_budget_receipts',
        'environment_workload_retirements',
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
        'mcp_readiness_attention_actions',
        'mcp_readiness_incidents',
        'mcp_readiness_maintenance_windows',
        'mcp_readiness_notification_actions',
        'app_slos',
        'notification_channels',
        'synthetic_checks',
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
        'project_environment_edge_policies',
        'project_environment_queue_runtime_sets',
        'project_environment_route_policies',
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
        'managed_postgres_connection_capacity',
        'data_api_runtime_connection_observations',
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
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $$;
DROP TRIGGER aa_project_workload_owner ON apps;
DROP FUNCTION bind_project_workload_owner();
DROP TRIGGER project_resource_owner ON projects;
DROP FUNCTION bind_project_resource_owner();
ALTER TABLE projects DROP CONSTRAINT projects_account_id_fkey;
ALTER TABLE projects ADD CONSTRAINT projects_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_release_sets DROP CONSTRAINT project_release_sets_account_id_fkey;
ALTER TABLE project_release_sets ADD CONSTRAINT project_release_sets_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environments DROP CONSTRAINT project_environments_account_id_fkey;
ALTER TABLE project_environments ADD CONSTRAINT project_environments_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_route_policies DROP CONSTRAINT project_environment_route_policies_account_id_fkey;
ALTER TABLE project_environment_route_policies ADD CONSTRAINT project_environment_route_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_qualifications DROP CONSTRAINT project_environment_qualifications_account_id_fkey;
ALTER TABLE project_environment_qualifications ADD CONSTRAINT project_environment_qualifications_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_promotions DROP CONSTRAINT project_environment_promotions_account_id_fkey;
ALTER TABLE project_environment_promotions ADD CONSTRAINT project_environment_promotions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_edge_policies DROP CONSTRAINT project_environment_edge_policies_account_id_fkey;
ALTER TABLE project_environment_edge_policies ADD CONSTRAINT project_environment_edge_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_config_versions DROP CONSTRAINT project_environment_config_versions_account_id_fkey;
ALTER TABLE project_environment_config_versions ADD CONSTRAINT project_environment_config_versions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_clone_operations DROP CONSTRAINT project_environment_clone_operations_account_id_fkey;
ALTER TABLE project_environment_clone_operations ADD CONSTRAINT project_environment_clone_operations_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_clone_configuration_guards DROP CONSTRAINT project_environment_clone_configuration_guards_account_id_fkey;
ALTER TABLE project_environment_clone_configuration_guards ADD CONSTRAINT project_environment_clone_configuration_guards_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_approvals DROP CONSTRAINT project_environment_approvals_account_id_fkey;
ALTER TABLE project_environment_approvals ADD CONSTRAINT project_environment_approvals_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE feature_flag_versions DROP CONSTRAINT feature_flag_versions_account_id_fkey;
ALTER TABLE feature_flag_versions ADD CONSTRAINT feature_flag_versions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE environment_workload_retirements DROP CONSTRAINT environment_workload_retirements_account_id_fkey;
ALTER TABLE environment_workload_retirements ADD CONSTRAINT environment_workload_retirements_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE environment_route_budget_receipts DROP CONSTRAINT environment_route_budget_receipts_account_id_fkey;
ALTER TABLE environment_route_budget_receipts ADD CONSTRAINT environment_route_budget_receipts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_work_policies DROP CONSTRAINT app_work_policies_account_id_fkey;
ALTER TABLE app_work_policies ADD CONSTRAINT app_work_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_environment_workload_intents DROP CONSTRAINT app_environment_workload_intents_account_id_fkey;
ALTER TABLE app_environment_workload_intents ADD CONSTRAINT app_environment_workload_intents_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT app_environment_secret_refs_account_id_fkey;
ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT app_environment_secret_ref_suppressions_account_id_fkey;
ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT app_environment_secret_ref_suppressions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_environment_route_budgets DROP CONSTRAINT app_environment_route_budgets_account_id_fkey;
ALTER TABLE app_environment_route_budgets ADD CONSTRAINT app_environment_route_budgets_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
DROP INDEX projects_org_slug_uniq;
ALTER TABLE projects ALTER COLUMN org_id DROP NOT NULL;
-- +goose StatementEnd
