-- migration-compat: contract
-- Organization project ownership (ADR-202610171905), applied online (ADR-202610171900, ADR-202610171908).
-- Replaces the single-transaction 20261018003100004, which never ran in production.
--
-- Each step is its own short transaction on at most one existing table (plus
-- the table a new foreign key references), waits at most 500ms for a lock
-- (below deadlock_timeout, so the migration is the side that yields) and
-- retries. Backfills commit every batch. Constraints are added NOT VALID and
-- validated separately. Every step is idempotent.
-- migration-lint: allow not-null projects.org_id: bind_project_resource_owner derives the organization for legacy INSERTs; the backfill is validated before SET NOT NULL, which then only reads the catalog.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '500ms';

-- ---------------------------------------------------------------- 1. ownership functions (triggers come after the backfill)
-- bind_project_resource_owner
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_project_resource_owner() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementEnd
-- bind_project_workload_owner
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_project_workload_owner() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementEnd

-- ---------------------------------------------------------------- 2. refuse mixed ownership, then backfill projects.org_id in batches
-- projects: refuse mixed ownership before changing anything
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM apps WHERE project_id IS NOT NULL GROUP BY project_id HAVING count(DISTINCT org_id)>1)
 OR EXISTS (SELECT 1 FROM apps a JOIN projects p ON p.id=a.project_id WHERE a.account_id<>p.account_id)
 OR EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id JOIN apps a ON a.project_id=p.id
            WHERE NOT o.personal_org AND a.org_id IS DISTINCT FROM p.org_id) THEN
  RAISE EXCEPTION 'remediate mixed project ownership before organization migration' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- projects: org_id follows the first app, else the existing or personal organization
DO $online$
DECLARE ids uuid[]; last bigint; found bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_project_targets;
  CREATE TEMP TABLE org_project_targets AS
   SELECT row_number() OVER () AS n, t.id, t.org_id FROM (SELECT p.id, coalesce(fa.org_id, p.org_id, po.id) AS org_id FROM projects p
   LEFT JOIN (SELECT DISTINCT ON (a.project_id) a.project_id, a.org_id FROM apps a
              WHERE a.project_id IS NOT NULL ORDER BY a.project_id, a.created_at, a.id) fa ON fa.project_id = p.id
   LEFT JOIN orgs po ON po.personal_org AND po.personal_owner_account_id = p.account_id
   WHERE p.org_id IS DISTINCT FROM coalesce(fa.org_id, p.org_id, po.id)) t;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_project_targets (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found <= 1000;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'projects: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(id) INTO ids FROM org_project_targets WHERE n > last AND n <= last + 500;
    UPDATE projects p SET org_id = t.org_id FROM org_project_targets t
     WHERE p.id IN (SELECT x.id FROM projects x WHERE x.id = ANY (ids) LIMIT 500)
      AND t.id = p.id AND p.org_id IS DISTINCT FROM t.org_id;
    last := last + 500;
    failures := 0;
   EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
    failures := failures + 1;
    IF failures > 600 THEN RAISE; END IF;
    PERFORM pg_sleep(0.05);
   END;
   PERFORM set_config('lock_timeout', '5s', true);
   COMMIT;
  END LOOP;
 END LOOP;
 DROP TABLE IF EXISTS pg_temp.org_project_targets;
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 3. ownership triggers
-- +goose StatementBegin
-- projects: catch up and install the ownership trigger under one short write lock
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   LOCK TABLE projects IN SHARE ROW EXCLUSIVE MODE;
   CREATE TEMP TABLE org_project_catch_up ON COMMIT DROP AS SELECT p.id, coalesce(fa.org_id, p.org_id, po.id) AS org_id FROM projects p
      LEFT JOIN (SELECT DISTINCT ON (a.project_id) a.project_id, a.org_id FROM apps a
                 WHERE a.project_id IS NOT NULL ORDER BY a.project_id, a.created_at, a.id) fa ON fa.project_id = p.id
      LEFT JOIN orgs po ON po.personal_org AND po.personal_owner_account_id = p.account_id
      WHERE p.org_id IS DISTINCT FROM coalesce(fa.org_id, p.org_id, po.id);
   IF (SELECT count(*) FROM org_project_catch_up) > 10000 THEN
    RAISE EXCEPTION 'projects: more rows changed during the backfill than one catch-up covers; run the migration again' USING ERRCODE = '55000';
   END IF;
   UPDATE projects p SET org_id = t.org_id FROM org_project_catch_up t
    WHERE p.id IN (SELECT x.id FROM projects x WHERE x.id IN (SELECT id FROM org_project_catch_up) LIMIT 10000)
     AND t.id = p.id;
   CREATE OR REPLACE TRIGGER project_resource_owner BEFORE INSERT OR UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION bind_project_resource_owner();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects: catch up and install the ownership trigger under one short write lock' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- apps: trigger aa_project_workload_owner
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER aa_project_workload_owner BEFORE INSERT OR UPDATE OF project_id,account_id,org_id ON apps
    FOR EACH ROW EXECUTE FUNCTION bind_project_workload_owner();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: trigger aa_project_workload_owner' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- org_app_identity_erasure_blocked
-- +goose StatementBegin
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
        'data_repair_rehearsals',
        'scenario_cold_preparations',
        'scenario_managed_work',
        'scenario_object_fixtures',
        'scenario_postgres_faults',
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
-- +goose StatementEnd
-- projects: no mixed ownership remains (writers are now fenced)
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM apps WHERE project_id IS NOT NULL GROUP BY project_id HAVING count(DISTINCT org_id)>1)
 OR EXISTS (SELECT 1 FROM apps a JOIN projects p ON p.id=a.project_id WHERE a.account_id<>p.account_id)
 OR EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id JOIN apps a ON a.project_id=p.id
            WHERE NOT o.personal_org AND a.org_id IS DISTINCT FROM p.org_id) THEN
  RAISE EXCEPTION 'remediate mixed project ownership before organization migration' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 4. constraints NOT VALID
-- +goose StatementBegin
-- projects.org_id: prove NOT NULL with a NOT VALID check
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'projects'::regclass AND attname = 'org_id' AND NOT attnotnull) AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'org_nn_4b15acc12ba1') THEN
    ALTER TABLE projects ADD CONSTRAINT org_nn_4b15acc12ba1 CHECK (org_id IS NOT NULL) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects.org_id: prove NOT NULL with a NOT VALID check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_deploy_rate_limits: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_deploy_rate_limits'::regclass AND conname = 'account_deploy_rate_limits_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_deploy_rate_limits'::regclass AND conname = 'org_ns_fk_3fb7e002d2de') THEN
    ALTER TABLE account_deploy_rate_limits ADD CONSTRAINT org_ns_fk_3fb7e002d2de FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: account_deploy_rate_limits: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_service_address_cursors: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_address_cursors'::regclass AND conname = 'app_service_address_cursors_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_address_cursors'::regclass AND conname = 'org_ns_fk_38844acaedc6') THEN
    ALTER TABLE app_service_address_cursors ADD CONSTRAINT org_ns_fk_38844acaedc6 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_service_address_cursors: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- idempotency_keys: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'idempotency_keys'::regclass AND conname = 'idempotency_keys_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'idempotency_keys'::regclass AND conname = 'org_ns_fk_392892fd3752') THEN
    ALTER TABLE idempotency_keys ADD CONSTRAINT org_ns_fk_392892fd3752 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: idempotency_keys: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_route_budgets: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_route_budgets'::regclass AND conname = 'app_environment_route_budgets_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_route_budgets'::regclass AND conname = 'org_ns_fk_3c4d89de182f') THEN
    ALTER TABLE app_environment_route_budgets ADD CONSTRAINT org_ns_fk_3c4d89de182f FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_route_budgets: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_secret_ref_suppressions: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_ref_suppressions'::regclass AND conname = 'app_environment_secret_ref_suppressions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_ref_suppressions'::regclass AND conname = 'org_ns_fk_46dea0ec624f') THEN
    ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT org_ns_fk_46dea0ec624f FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_secret_ref_suppressions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_secret_refs: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'app_environment_secret_refs_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'org_ns_fk_7cb5e6214343') THEN
    ALTER TABLE app_environment_secret_refs ADD CONSTRAINT org_ns_fk_7cb5e6214343 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_secret_refs: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_workload_intents: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_workload_intents'::regclass AND conname = 'app_environment_workload_intents_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_workload_intents'::regclass AND conname = 'org_ns_fk_8191cc35ab6e') THEN
    ALTER TABLE app_environment_workload_intents ADD CONSTRAINT org_ns_fk_8191cc35ab6e FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_workload_intents: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_work_policies: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_work_policies'::regclass AND conname = 'app_work_policies_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_work_policies'::regclass AND conname = 'org_ns_fk_f1d06aa1dbab') THEN
    ALTER TABLE app_work_policies ADD CONSTRAINT org_ns_fk_f1d06aa1dbab FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_work_policies: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- environment_route_budget_receipts: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_route_budget_receipts'::regclass AND conname = 'environment_route_budget_receipts_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_route_budget_receipts'::regclass AND conname = 'org_ns_fk_4e91582b6a9c') THEN
    ALTER TABLE environment_route_budget_receipts ADD CONSTRAINT org_ns_fk_4e91582b6a9c FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: environment_route_budget_receipts: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- environment_workload_retirements: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_workload_retirements'::regclass AND conname = 'environment_workload_retirements_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_workload_retirements'::regclass AND conname = 'org_ns_fk_155fbae99a91') THEN
    ALTER TABLE environment_workload_retirements ADD CONSTRAINT org_ns_fk_155fbae99a91 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: environment_workload_retirements: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- feature_flag_versions: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'feature_flag_versions'::regclass AND conname = 'feature_flag_versions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'feature_flag_versions'::regclass AND conname = 'org_ns_fk_3590a80d0dae') THEN
    ALTER TABLE feature_flag_versions ADD CONSTRAINT org_ns_fk_3590a80d0dae FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: feature_flag_versions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_approvals: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'project_environment_approvals_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'org_ns_fk_2da301c91737') THEN
    ALTER TABLE project_environment_approvals ADD CONSTRAINT org_ns_fk_2da301c91737 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_approvals: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_clone_configuration_guards: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_configuration_guards'::regclass AND conname = 'project_environment_clone_configuration_guards_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_configuration_guards'::regclass AND conname = 'org_ns_fk_7f552dd1a20c') THEN
    ALTER TABLE project_environment_clone_configuration_guards ADD CONSTRAINT org_ns_fk_7f552dd1a20c FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_clone_configuration_guards: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_clone_operations: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_operations'::regclass AND conname = 'project_environment_clone_operations_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_operations'::regclass AND conname = 'org_ns_fk_be2e2b340125') THEN
    ALTER TABLE project_environment_clone_operations ADD CONSTRAINT org_ns_fk_be2e2b340125 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_clone_operations: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_config_versions: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_config_versions'::regclass AND conname = 'project_environment_config_versions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_config_versions'::regclass AND conname = 'org_ns_fk_620a5427b2ab') THEN
    ALTER TABLE project_environment_config_versions ADD CONSTRAINT org_ns_fk_620a5427b2ab FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_config_versions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_edge_policies: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_edge_policies'::regclass AND conname = 'project_environment_edge_policies_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_edge_policies'::regclass AND conname = 'org_ns_fk_df5726f62d16') THEN
    ALTER TABLE project_environment_edge_policies ADD CONSTRAINT org_ns_fk_df5726f62d16 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_edge_policies: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_promotions: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_promotions'::regclass AND conname = 'project_environment_promotions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_promotions'::regclass AND conname = 'org_ns_fk_3a4bf5c6d7be') THEN
    ALTER TABLE project_environment_promotions ADD CONSTRAINT org_ns_fk_3a4bf5c6d7be FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_promotions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_qualifications: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_qualifications'::regclass AND conname = 'project_environment_qualifications_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_qualifications'::regclass AND conname = 'org_ns_fk_2c73925429ac') THEN
    ALTER TABLE project_environment_qualifications ADD CONSTRAINT org_ns_fk_2c73925429ac FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_qualifications: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_route_policies: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_route_policies'::regclass AND conname = 'project_environment_route_policies_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_route_policies'::regclass AND conname = 'org_ns_fk_eb3fb5dd5116') THEN
    ALTER TABLE project_environment_route_policies ADD CONSTRAINT org_ns_fk_eb3fb5dd5116 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_route_policies: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environments: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environments'::regclass AND conname = 'project_environments_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environments'::regclass AND conname = 'org_ns_fk_fdd052165436') THEN
    ALTER TABLE project_environments ADD CONSTRAINT org_ns_fk_fdd052165436 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_release_sets: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_release_sets'::regclass AND conname = 'project_release_sets_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_release_sets'::regclass AND conname = 'org_ns_fk_66f3d9c1a12c') THEN
    ALTER TABLE project_release_sets ADD CONSTRAINT org_ns_fk_66f3d9c1a12c FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_release_sets: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- projects: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'projects_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'org_ns_fk_35a2864c314e') THEN
    ALTER TABLE projects ADD CONSTRAINT org_ns_fk_35a2864c314e FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 5. validate
-- +goose StatementBegin
-- projects: validate org_nn_4b15acc12ba1
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'org_nn_4b15acc12ba1' AND NOT convalidated) THEN
    ALTER TABLE projects VALIDATE CONSTRAINT org_nn_4b15acc12ba1;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects: validate org_nn_4b15acc12ba1' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_deploy_rate_limits: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_deploy_rate_limits'::regclass AND conname = 'org_ns_fk_3fb7e002d2de' AND NOT convalidated) THEN
    ALTER TABLE account_deploy_rate_limits VALIDATE CONSTRAINT org_ns_fk_3fb7e002d2de;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: account_deploy_rate_limits: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_service_address_cursors: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_address_cursors'::regclass AND conname = 'org_ns_fk_38844acaedc6' AND NOT convalidated) THEN
    ALTER TABLE app_service_address_cursors VALIDATE CONSTRAINT org_ns_fk_38844acaedc6;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_service_address_cursors: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- idempotency_keys: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'idempotency_keys'::regclass AND conname = 'org_ns_fk_392892fd3752' AND NOT convalidated) THEN
    ALTER TABLE idempotency_keys VALIDATE CONSTRAINT org_ns_fk_392892fd3752;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: idempotency_keys: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_route_budgets: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_route_budgets'::regclass AND conname = 'org_ns_fk_3c4d89de182f' AND NOT convalidated) THEN
    ALTER TABLE app_environment_route_budgets VALIDATE CONSTRAINT org_ns_fk_3c4d89de182f;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_route_budgets: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_secret_ref_suppressions: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_ref_suppressions'::regclass AND conname = 'org_ns_fk_46dea0ec624f' AND NOT convalidated) THEN
    ALTER TABLE app_environment_secret_ref_suppressions VALIDATE CONSTRAINT org_ns_fk_46dea0ec624f;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_secret_ref_suppressions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_secret_refs: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'org_ns_fk_7cb5e6214343' AND NOT convalidated) THEN
    ALTER TABLE app_environment_secret_refs VALIDATE CONSTRAINT org_ns_fk_7cb5e6214343;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_secret_refs: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_workload_intents: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_workload_intents'::regclass AND conname = 'org_ns_fk_8191cc35ab6e' AND NOT convalidated) THEN
    ALTER TABLE app_environment_workload_intents VALIDATE CONSTRAINT org_ns_fk_8191cc35ab6e;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_workload_intents: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_work_policies: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_work_policies'::regclass AND conname = 'org_ns_fk_f1d06aa1dbab' AND NOT convalidated) THEN
    ALTER TABLE app_work_policies VALIDATE CONSTRAINT org_ns_fk_f1d06aa1dbab;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_work_policies: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- environment_route_budget_receipts: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_route_budget_receipts'::regclass AND conname = 'org_ns_fk_4e91582b6a9c' AND NOT convalidated) THEN
    ALTER TABLE environment_route_budget_receipts VALIDATE CONSTRAINT org_ns_fk_4e91582b6a9c;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: environment_route_budget_receipts: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- environment_workload_retirements: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_workload_retirements'::regclass AND conname = 'org_ns_fk_155fbae99a91' AND NOT convalidated) THEN
    ALTER TABLE environment_workload_retirements VALIDATE CONSTRAINT org_ns_fk_155fbae99a91;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: environment_workload_retirements: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- feature_flag_versions: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'feature_flag_versions'::regclass AND conname = 'org_ns_fk_3590a80d0dae' AND NOT convalidated) THEN
    ALTER TABLE feature_flag_versions VALIDATE CONSTRAINT org_ns_fk_3590a80d0dae;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: feature_flag_versions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_approvals: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'org_ns_fk_2da301c91737' AND NOT convalidated) THEN
    ALTER TABLE project_environment_approvals VALIDATE CONSTRAINT org_ns_fk_2da301c91737;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_approvals: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_clone_configuration_guards: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_configuration_guards'::regclass AND conname = 'org_ns_fk_7f552dd1a20c' AND NOT convalidated) THEN
    ALTER TABLE project_environment_clone_configuration_guards VALIDATE CONSTRAINT org_ns_fk_7f552dd1a20c;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_clone_configuration_guards: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_clone_operations: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_operations'::regclass AND conname = 'org_ns_fk_be2e2b340125' AND NOT convalidated) THEN
    ALTER TABLE project_environment_clone_operations VALIDATE CONSTRAINT org_ns_fk_be2e2b340125;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_clone_operations: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_config_versions: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_config_versions'::regclass AND conname = 'org_ns_fk_620a5427b2ab' AND NOT convalidated) THEN
    ALTER TABLE project_environment_config_versions VALIDATE CONSTRAINT org_ns_fk_620a5427b2ab;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_config_versions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_edge_policies: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_edge_policies'::regclass AND conname = 'org_ns_fk_df5726f62d16' AND NOT convalidated) THEN
    ALTER TABLE project_environment_edge_policies VALIDATE CONSTRAINT org_ns_fk_df5726f62d16;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_edge_policies: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_promotions: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_promotions'::regclass AND conname = 'org_ns_fk_3a4bf5c6d7be' AND NOT convalidated) THEN
    ALTER TABLE project_environment_promotions VALIDATE CONSTRAINT org_ns_fk_3a4bf5c6d7be;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_promotions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_qualifications: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_qualifications'::regclass AND conname = 'org_ns_fk_2c73925429ac' AND NOT convalidated) THEN
    ALTER TABLE project_environment_qualifications VALIDATE CONSTRAINT org_ns_fk_2c73925429ac;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_qualifications: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_route_policies: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_route_policies'::regclass AND conname = 'org_ns_fk_eb3fb5dd5116' AND NOT convalidated) THEN
    ALTER TABLE project_environment_route_policies VALIDATE CONSTRAINT org_ns_fk_eb3fb5dd5116;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_route_policies: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environments: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environments'::regclass AND conname = 'org_ns_fk_fdd052165436' AND NOT convalidated) THEN
    ALTER TABLE project_environments VALIDATE CONSTRAINT org_ns_fk_fdd052165436;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_release_sets: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_release_sets'::regclass AND conname = 'org_ns_fk_66f3d9c1a12c' AND NOT convalidated) THEN
    ALTER TABLE project_release_sets VALIDATE CONSTRAINT org_ns_fk_66f3d9c1a12c;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_release_sets: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- projects: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'org_ns_fk_35a2864c314e' AND NOT convalidated) THEN
    ALTER TABLE projects VALIDATE CONSTRAINT org_ns_fk_35a2864c314e;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 6. retire replaced constraints, tighten, index
-- +goose StatementBegin
-- account_deploy_rate_limits: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_deploy_rate_limits'::regclass AND conname = 'org_ns_fk_3fb7e002d2de') THEN
    ALTER TABLE account_deploy_rate_limits DROP CONSTRAINT IF EXISTS account_deploy_rate_limits_account_id_fkey;
    ALTER TABLE account_deploy_rate_limits RENAME CONSTRAINT org_ns_fk_3fb7e002d2de TO account_deploy_rate_limits_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: account_deploy_rate_limits: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_service_address_cursors: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_address_cursors'::regclass AND conname = 'org_ns_fk_38844acaedc6') THEN
    ALTER TABLE app_service_address_cursors DROP CONSTRAINT IF EXISTS app_service_address_cursors_account_id_fkey;
    ALTER TABLE app_service_address_cursors RENAME CONSTRAINT org_ns_fk_38844acaedc6 TO app_service_address_cursors_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_service_address_cursors: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- idempotency_keys: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'idempotency_keys'::regclass AND conname = 'org_ns_fk_392892fd3752') THEN
    ALTER TABLE idempotency_keys DROP CONSTRAINT IF EXISTS idempotency_keys_account_id_fkey;
    ALTER TABLE idempotency_keys RENAME CONSTRAINT org_ns_fk_392892fd3752 TO idempotency_keys_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: idempotency_keys: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_route_budgets: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_route_budgets'::regclass AND conname = 'org_ns_fk_3c4d89de182f') THEN
    ALTER TABLE app_environment_route_budgets DROP CONSTRAINT IF EXISTS app_environment_route_budgets_account_id_fkey;
    ALTER TABLE app_environment_route_budgets RENAME CONSTRAINT org_ns_fk_3c4d89de182f TO app_environment_route_budgets_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_route_budgets: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_secret_ref_suppressions: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_ref_suppressions'::regclass AND conname = 'org_ns_fk_46dea0ec624f') THEN
    ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT IF EXISTS app_environment_secret_ref_suppressions_account_id_fkey;
    ALTER TABLE app_environment_secret_ref_suppressions RENAME CONSTRAINT org_ns_fk_46dea0ec624f TO app_environment_secret_ref_suppressions_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_secret_ref_suppressions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_secret_refs: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'org_ns_fk_7cb5e6214343') THEN
    ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_account_id_fkey;
    ALTER TABLE app_environment_secret_refs RENAME CONSTRAINT org_ns_fk_7cb5e6214343 TO app_environment_secret_refs_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_secret_refs: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_environment_workload_intents: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_workload_intents'::regclass AND conname = 'org_ns_fk_8191cc35ab6e') THEN
    ALTER TABLE app_environment_workload_intents DROP CONSTRAINT IF EXISTS app_environment_workload_intents_account_id_fkey;
    ALTER TABLE app_environment_workload_intents RENAME CONSTRAINT org_ns_fk_8191cc35ab6e TO app_environment_workload_intents_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_environment_workload_intents: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_work_policies: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_work_policies'::regclass AND conname = 'org_ns_fk_f1d06aa1dbab') THEN
    ALTER TABLE app_work_policies DROP CONSTRAINT IF EXISTS app_work_policies_account_id_fkey;
    ALTER TABLE app_work_policies RENAME CONSTRAINT org_ns_fk_f1d06aa1dbab TO app_work_policies_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: app_work_policies: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- environment_route_budget_receipts: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_route_budget_receipts'::regclass AND conname = 'org_ns_fk_4e91582b6a9c') THEN
    ALTER TABLE environment_route_budget_receipts DROP CONSTRAINT IF EXISTS environment_route_budget_receipts_account_id_fkey;
    ALTER TABLE environment_route_budget_receipts RENAME CONSTRAINT org_ns_fk_4e91582b6a9c TO environment_route_budget_receipts_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: environment_route_budget_receipts: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- environment_workload_retirements: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_workload_retirements'::regclass AND conname = 'org_ns_fk_155fbae99a91') THEN
    ALTER TABLE environment_workload_retirements DROP CONSTRAINT IF EXISTS environment_workload_retirements_account_id_fkey;
    ALTER TABLE environment_workload_retirements RENAME CONSTRAINT org_ns_fk_155fbae99a91 TO environment_workload_retirements_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: environment_workload_retirements: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- feature_flag_versions: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'feature_flag_versions'::regclass AND conname = 'org_ns_fk_3590a80d0dae') THEN
    ALTER TABLE feature_flag_versions DROP CONSTRAINT IF EXISTS feature_flag_versions_account_id_fkey;
    ALTER TABLE feature_flag_versions RENAME CONSTRAINT org_ns_fk_3590a80d0dae TO feature_flag_versions_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: feature_flag_versions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_approvals: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_approvals'::regclass AND conname = 'org_ns_fk_2da301c91737') THEN
    ALTER TABLE project_environment_approvals DROP CONSTRAINT IF EXISTS project_environment_approvals_account_id_fkey;
    ALTER TABLE project_environment_approvals RENAME CONSTRAINT org_ns_fk_2da301c91737 TO project_environment_approvals_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_approvals: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_clone_configuration_guards: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_configuration_guards'::regclass AND conname = 'org_ns_fk_7f552dd1a20c') THEN
    ALTER TABLE project_environment_clone_configuration_guards DROP CONSTRAINT IF EXISTS project_environment_clone_configuration_guards_account_id_fkey;
    ALTER TABLE project_environment_clone_configuration_guards RENAME CONSTRAINT org_ns_fk_7f552dd1a20c TO project_environment_clone_configuration_guards_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_clone_configuration_guards: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_clone_operations: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_clone_operations'::regclass AND conname = 'org_ns_fk_be2e2b340125') THEN
    ALTER TABLE project_environment_clone_operations DROP CONSTRAINT IF EXISTS project_environment_clone_operations_account_id_fkey;
    ALTER TABLE project_environment_clone_operations RENAME CONSTRAINT org_ns_fk_be2e2b340125 TO project_environment_clone_operations_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_clone_operations: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_config_versions: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_config_versions'::regclass AND conname = 'org_ns_fk_620a5427b2ab') THEN
    ALTER TABLE project_environment_config_versions DROP CONSTRAINT IF EXISTS project_environment_config_versions_account_id_fkey;
    ALTER TABLE project_environment_config_versions RENAME CONSTRAINT org_ns_fk_620a5427b2ab TO project_environment_config_versions_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_config_versions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_edge_policies: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_edge_policies'::regclass AND conname = 'org_ns_fk_df5726f62d16') THEN
    ALTER TABLE project_environment_edge_policies DROP CONSTRAINT IF EXISTS project_environment_edge_policies_account_id_fkey;
    ALTER TABLE project_environment_edge_policies RENAME CONSTRAINT org_ns_fk_df5726f62d16 TO project_environment_edge_policies_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_edge_policies: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_promotions: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_promotions'::regclass AND conname = 'org_ns_fk_3a4bf5c6d7be') THEN
    ALTER TABLE project_environment_promotions DROP CONSTRAINT IF EXISTS project_environment_promotions_account_id_fkey;
    ALTER TABLE project_environment_promotions RENAME CONSTRAINT org_ns_fk_3a4bf5c6d7be TO project_environment_promotions_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_promotions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_qualifications: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_qualifications'::regclass AND conname = 'org_ns_fk_2c73925429ac') THEN
    ALTER TABLE project_environment_qualifications DROP CONSTRAINT IF EXISTS project_environment_qualifications_account_id_fkey;
    ALTER TABLE project_environment_qualifications RENAME CONSTRAINT org_ns_fk_2c73925429ac TO project_environment_qualifications_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_qualifications: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_route_policies: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_route_policies'::regclass AND conname = 'org_ns_fk_eb3fb5dd5116') THEN
    ALTER TABLE project_environment_route_policies DROP CONSTRAINT IF EXISTS project_environment_route_policies_account_id_fkey;
    ALTER TABLE project_environment_route_policies RENAME CONSTRAINT org_ns_fk_eb3fb5dd5116 TO project_environment_route_policies_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_route_policies: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environments: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environments'::regclass AND conname = 'org_ns_fk_fdd052165436') THEN
    ALTER TABLE project_environments DROP CONSTRAINT IF EXISTS project_environments_account_id_fkey;
    ALTER TABLE project_environments RENAME CONSTRAINT org_ns_fk_fdd052165436 TO project_environments_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_release_sets: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_release_sets'::regclass AND conname = 'org_ns_fk_66f3d9c1a12c') THEN
    ALTER TABLE project_release_sets DROP CONSTRAINT IF EXISTS project_release_sets_account_id_fkey;
    ALTER TABLE project_release_sets RENAME CONSTRAINT org_ns_fk_66f3d9c1a12c TO project_release_sets_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_release_sets: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- projects: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'projects'::regclass AND conname = 'org_ns_fk_35a2864c314e') THEN
    ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_account_id_fkey;
    ALTER TABLE projects RENAME CONSTRAINT org_ns_fk_35a2864c314e TO projects_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- projects.org_id: SET NOT NULL from the validated check
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'projects'::regclass AND attname = 'org_id' AND NOT attnotnull) THEN
    ALTER TABLE projects ALTER COLUMN org_id SET NOT NULL;
   END IF;
   ALTER TABLE projects DROP CONSTRAINT IF EXISTS org_nn_4b15acc12ba1;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects.org_id: SET NOT NULL from the validated check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- projects: refuse duplicate slugs within an organization
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM projects GROUP BY org_id, slug HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'remediate duplicate project slugs within an organization before migration' USING ERRCODE = '23505';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- projects_org_slug_uniq: drop an interrupted build
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('projects_org_slug_uniq') AND NOT indisvalid) THEN
    DROP INDEX projects_org_slug_uniq;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: projects_org_slug_uniq: drop an interrupted build' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- projects_org_slug_uniq: build concurrently (waits only for older transactions)
SET lock_timeout = '10s';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS projects_org_slug_uniq ON projects (org_id,slug);
SET lock_timeout = '500ms';
RESET lock_timeout;

-- +goose Down
-- Rollback is an operator action on a drained schema, not part of a release:
-- releases roll back by running the previous binaries against this schema.
SET lock_timeout = '5s';
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
        'data_repair_rehearsals',
        'scenario_cold_preparations',
        'scenario_managed_work',
        'scenario_object_fixtures',
        'scenario_postgres_faults',
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
RESET lock_timeout;
