-- migration-compat: contract
-- Organization managed PostgreSQL ownership (ADR-202610171906), applied online (ADR-202610171900, ADR-202610171908).
-- Replaces the single-transaction 20261018003100005, which never ran in production.
--
-- Each step is its own short transaction on at most one existing table (plus
-- the table a new foreign key references), waits at most 500ms for a lock
-- (below deadlock_timeout, so the migration is the side that yields) and
-- retries. Backfills commit every batch. Constraints are added NOT VALID and
-- validated separately. Every step is idempotent.
-- migration-lint: allow not-null managed_postgres_databases.org_id: bind_managed_postgres_resource_owner derives existing organization/restore/personal roots for legacy INSERTs after the validated backfill.
-- migration-lint: allow not-null managed_postgres_bindings.database_account_id: the binding owner guard derives this namespace from database_id for legacy INSERTs; it is never guessed from the caller.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '500ms';

-- ---------------------------------------------------------------- 1. new nullable columns
-- +goose StatementBegin
-- managed_postgres_databases: org_id column
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS org_id uuid;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: org_id column' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: database_account_id column
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE managed_postgres_bindings ADD COLUMN IF NOT EXISTS database_account_id uuid;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: database_account_id column' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 2. functions that do not depend on the backfill
-- bind_managed_postgres_resource_owner
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_managed_postgres_resource_owner() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementEnd
-- org_owner_identity_active
-- +goose StatementBegin
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
-- +goose StatementEnd
-- org_streaming_plan_guard
-- +goose StatementBegin
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
-- +goose StatementEnd
-- pg_tier_rank
-- +goose StatementBegin
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
-- +goose StatementEnd
-- managed_postgres_usage_subject
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION managed_postgres_usage_subject(owner_org uuid, namespace uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE namespace END FROM orgs o WHERE o.id=owner_org
$$;
-- +goose StatementEnd
-- guard_org_managed_postgres_databases
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_org_managed_postgres_databases() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.deleted_pending OR NEW.status='deleted_pending') AND EXISTS(
     SELECT 1 FROM managed_postgres_databases WHERE org_id=NEW.id AND state<>'deleted') THEN
  RAISE EXCEPTION 'organization has managed PostgreSQL databases' USING ERRCODE='23514',CONSTRAINT='org_has_managed_postgres_databases';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 3. refuse mixed roots, then backfill in batches
-- +goose StatementBegin
-- managed_postgres_databases: org_id from binding and restore ancestry, else personal
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 DROP TABLE IF EXISTS pg_temp.managed_postgres_owner_roots;
 CREATE TEMP TABLE managed_postgres_owner_roots ON COMMIT PRESERVE ROWS AS
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
 IF EXISTS(SELECT 1 FROM managed_postgres_owner_roots WHERE owner_count<>1 OR org_id IS NULL) THEN
  RAISE EXCEPTION 'remediate mixed managed PostgreSQL ownership before migration' USING ERRCODE='23514';
 END IF;
 PERFORM set_config('lock_timeout', '5s', true);
 COMMIT;
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM managed_postgres_databases
    WHERE org_id IS NULL AND id > last ORDER BY id LIMIT 500) batch;
   IF ids IS NOT NULL THEN
    UPDATE managed_postgres_databases d SET org_id = coalesce((SELECT r.org_id FROM managed_postgres_owner_roots r WHERE r.id=d.id), (SELECT o.id FROM orgs o WHERE o.personal_org AND o.personal_owner_account_id=d.account_id)) WHERE d.id = ANY (ids) AND d.org_id IS NULL;
    last := ids[cardinality(ids)];
   END IF;
   failures := 0;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   failures := failures + 1;
   IF failures > 600 THEN RAISE; END IF;
   ids := ARRAY[]::uuid[];
   PERFORM pg_sleep(0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN ids IS NULL;
 END LOOP;
 DROP TABLE managed_postgres_owner_roots;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: database_account_id from the database
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM managed_postgres_bindings WHERE database_account_id IS NULL AND id > last ORDER BY id LIMIT 500) batch;
   IF ids IS NOT NULL THEN
    UPDATE managed_postgres_bindings b SET database_account_id = d.account_id FROM managed_postgres_databases d WHERE d.id = b.database_id AND b.id = ANY (ids) AND b.database_account_id IS NULL;
    last := ids[cardinality(ids)];
   END IF;
   failures := 0;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   failures := failures + 1;
   IF failures > 600 THEN RAISE; END IF;
   ids := ARRAY[]::uuid[];
   PERFORM pg_sleep(0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN ids IS NULL;
 END LOOP;
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 4. ownership triggers and the functions that read the backfilled columns
-- +goose StatementBegin
-- managed_postgres_databases: catch up and install the ownership trigger under one short write lock
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   LOCK TABLE managed_postgres_databases IN SHARE ROW EXCLUSIVE MODE;
   IF EXISTS (SELECT 1 FROM managed_postgres_databases WHERE org_id IS NULL) THEN
    DROP TABLE IF EXISTS pg_temp.managed_postgres_owner_roots;
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
    UPDATE managed_postgres_databases d SET org_id = coalesce((SELECT r.org_id FROM managed_postgres_owner_roots r WHERE r.id=d.id), (SELECT o.id FROM orgs o WHERE o.personal_org AND o.personal_owner_account_id=d.account_id)) WHERE d.org_id IS NULL;
   END IF;
   CREATE OR REPLACE TRIGGER managed_postgres_resource_owner BEFORE INSERT OR UPDATE OF org_id,account_id,restore_source_database_id ON managed_postgres_databases
    FOR EACH ROW EXECUTE FUNCTION bind_managed_postgres_resource_owner();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: catch up and install the ownership trigger under one short write lock' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- guard_managed_postgres_binding_owner
-- +goose StatementBegin
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
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: catch up and widen the owner guard under one short write lock
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   LOCK TABLE managed_postgres_bindings IN SHARE ROW EXCLUSIVE MODE;
   UPDATE managed_postgres_bindings b SET database_account_id = d.account_id FROM managed_postgres_databases d
    WHERE d.id = b.database_id AND b.database_account_id IS NULL;
   CREATE OR REPLACE TRIGGER managed_postgres_binding_owner_guard BEFORE INSERT OR UPDATE OF account_id,database_id,app_id,database_account_id ON managed_postgres_bindings
    FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_binding_owner();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: catch up and widen the owner guard under one short write lock' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- guard_account_managed_postgres_databases
-- +goose StatementBegin
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
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: trigger org_managed_postgres_databases_guard
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_managed_postgres_databases_guard BEFORE UPDATE OF status,deleted_pending ON orgs
    FOR EACH ROW EXECUTE FUNCTION guard_org_managed_postgres_databases();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_managed_postgres_databases_guard' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- org_app_identity_erasure_blocked
-- +goose StatementBegin
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
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $_$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 5. constraints NOT VALID
-- +goose StatementBegin
-- managed_postgres_databases: add managed_postgres_databases_org_id_fkey NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'managed_postgres_databases_org_id_fkey') THEN
    ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_org_id_fkey FOREIGN KEY (org_id) REFERENCES orgs(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: add managed_postgres_databases_org_id_fkey NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: add managed_postgres_bindings_database_account_id_fkey NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_database_account_id_fkey') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_database_account_id_fkey FOREIGN KEY (database_account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: add managed_postgres_bindings_database_account_id_fkey NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases.org_id: prove NOT NULL with a NOT VALID check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'managed_postgres_databases'::regclass AND attname = 'org_id' AND NOT attnotnull) AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'org_nn_d1a8c4e90b3e') THEN
    ALTER TABLE managed_postgres_databases ADD CONSTRAINT org_nn_d1a8c4e90b3e CHECK (org_id IS NOT NULL) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases.org_id: prove NOT NULL with a NOT VALID check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings.database_account_id: prove NOT NULL with a NOT VALID check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'managed_postgres_bindings'::regclass AND attname = 'database_account_id' AND NOT attnotnull) AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'org_nn_180774322761') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT org_nn_180774322761 CHECK (database_account_id IS NOT NULL) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings.database_account_id: prove NOT NULL with a NOT VALID check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_accounting_reconciliations: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_accounting_reconciliations'::regclass AND conname = 'managed_postgres_accounting_reconciliations_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_accounting_reconciliations'::regclass AND conname = 'org_ns_fk_f79356dd3944') THEN
    ALTER TABLE managed_postgres_accounting_reconciliations ADD CONSTRAINT org_ns_fk_f79356dd3944 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_accounting_reconciliations: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'org_ns_fk_5eb275749f12') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT org_ns_fk_5eb275749f12 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_creation_receipts: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_creation_receipts'::regclass AND conname = 'managed_postgres_creation_receipts_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_creation_receipts'::regclass AND conname = 'org_ns_fk_e7398f11de92') THEN
    ALTER TABLE managed_postgres_creation_receipts ADD CONSTRAINT org_ns_fk_e7398f11de92 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE RESTRICT NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_creation_receipts: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_cutovers: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'managed_postgres_cutovers_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'org_ns_fk_ce739fe74978') THEN
    ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT org_ns_fk_ce739fe74978 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_cutovers: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'managed_postgres_databases_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'org_ns_fk_df30c182246b') THEN
    ALTER TABLE managed_postgres_databases ADD CONSTRAINT org_ns_fk_df30c182246b FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_health: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_health'::regclass AND conname = 'managed_postgres_health_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_health'::regclass AND conname = 'org_ns_fk_aafd96e0959c') THEN
    ALTER TABLE managed_postgres_health ADD CONSTRAINT org_ns_fk_aafd96e0959c FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_health: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_resizes: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'managed_postgres_resizes_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'org_ns_fk_2b4850ad12b7') THEN
    ALTER TABLE managed_postgres_resizes ADD CONSTRAINT org_ns_fk_2b4850ad12b7 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_resizes: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_restore_verifications: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_restore_verifications'::regclass AND conname = 'managed_postgres_restore_verifications_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_restore_verifications'::regclass AND conname = 'org_ns_fk_b24b26192e79') THEN
    ALTER TABLE managed_postgres_restore_verifications ADD CONSTRAINT org_ns_fk_b24b26192e79 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE RESTRICT NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_restore_verifications: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_usage: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'managed_postgres_usage_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'org_ns_fk_535e19141cd6') THEN
    ALTER TABLE managed_postgres_usage ADD CONSTRAINT org_ns_fk_535e19141cd6 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_usage: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_usage_imports: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage_imports'::regclass AND conname = 'managed_postgres_usage_imports_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage_imports'::regclass AND conname = 'org_ns_fk_0c4aa7699d86') THEN
    ALTER TABLE managed_postgres_usage_imports ADD CONSTRAINT org_ns_fk_0c4aa7699d86 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_usage_imports: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 6. validate
-- +goose StatementBegin
-- managed_postgres_databases: validate managed_postgres_databases_org_id_fkey
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'managed_postgres_databases_org_id_fkey' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT managed_postgres_databases_org_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: validate managed_postgres_databases_org_id_fkey' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: validate managed_postgres_bindings_database_account_id_fkey
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_database_account_id_fkey' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT managed_postgres_bindings_database_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: validate managed_postgres_bindings_database_account_id_fkey' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases: validate org_nn_d1a8c4e90b3e
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'org_nn_d1a8c4e90b3e' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT org_nn_d1a8c4e90b3e;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: validate org_nn_d1a8c4e90b3e' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: validate org_nn_180774322761
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'org_nn_180774322761' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT org_nn_180774322761;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: validate org_nn_180774322761' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_accounting_reconciliations: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_accounting_reconciliations'::regclass AND conname = 'org_ns_fk_f79356dd3944' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_accounting_reconciliations VALIDATE CONSTRAINT org_ns_fk_f79356dd3944;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_accounting_reconciliations: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'org_ns_fk_5eb275749f12' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT org_ns_fk_5eb275749f12;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_creation_receipts: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_creation_receipts'::regclass AND conname = 'org_ns_fk_e7398f11de92' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_creation_receipts VALIDATE CONSTRAINT org_ns_fk_e7398f11de92;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_creation_receipts: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_cutovers: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'org_ns_fk_ce739fe74978' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_cutovers VALIDATE CONSTRAINT org_ns_fk_ce739fe74978;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_cutovers: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'org_ns_fk_df30c182246b' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT org_ns_fk_df30c182246b;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_health: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_health'::regclass AND conname = 'org_ns_fk_aafd96e0959c' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_health VALIDATE CONSTRAINT org_ns_fk_aafd96e0959c;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_health: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_resizes: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'org_ns_fk_2b4850ad12b7' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT org_ns_fk_2b4850ad12b7;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_resizes: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_restore_verifications: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_restore_verifications'::regclass AND conname = 'org_ns_fk_b24b26192e79' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_restore_verifications VALIDATE CONSTRAINT org_ns_fk_b24b26192e79;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_restore_verifications: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_usage: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'org_ns_fk_535e19141cd6' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_usage VALIDATE CONSTRAINT org_ns_fk_535e19141cd6;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_usage: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_usage_imports: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage_imports'::regclass AND conname = 'org_ns_fk_0c4aa7699d86' AND NOT convalidated) THEN
    ALTER TABLE managed_postgres_usage_imports VALIDATE CONSTRAINT org_ns_fk_0c4aa7699d86;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_usage_imports: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 7. retire replaced constraints, tighten, index
-- +goose StatementBegin
-- managed_postgres_accounting_reconciliations: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_accounting_reconciliations'::regclass AND conname = 'org_ns_fk_f79356dd3944') THEN
    ALTER TABLE managed_postgres_accounting_reconciliations DROP CONSTRAINT IF EXISTS managed_postgres_accounting_reconciliations_account_id_fkey;
    ALTER TABLE managed_postgres_accounting_reconciliations RENAME CONSTRAINT org_ns_fk_f79356dd3944 TO managed_postgres_accounting_reconciliations_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_accounting_reconciliations: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'org_ns_fk_5eb275749f12') THEN
    ALTER TABLE managed_postgres_bindings DROP CONSTRAINT IF EXISTS managed_postgres_bindings_account_id_fkey;
    ALTER TABLE managed_postgres_bindings RENAME CONSTRAINT org_ns_fk_5eb275749f12 TO managed_postgres_bindings_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_creation_receipts: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_creation_receipts'::regclass AND conname = 'org_ns_fk_e7398f11de92') THEN
    ALTER TABLE managed_postgres_creation_receipts DROP CONSTRAINT IF EXISTS managed_postgres_creation_receipts_account_id_fkey;
    ALTER TABLE managed_postgres_creation_receipts RENAME CONSTRAINT org_ns_fk_e7398f11de92 TO managed_postgres_creation_receipts_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_creation_receipts: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_cutovers: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'org_ns_fk_ce739fe74978') THEN
    ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT IF EXISTS managed_postgres_cutovers_account_id_fkey;
    ALTER TABLE managed_postgres_cutovers RENAME CONSTRAINT org_ns_fk_ce739fe74978 TO managed_postgres_cutovers_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_cutovers: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'org_ns_fk_df30c182246b') THEN
    ALTER TABLE managed_postgres_databases DROP CONSTRAINT IF EXISTS managed_postgres_databases_account_id_fkey;
    ALTER TABLE managed_postgres_databases RENAME CONSTRAINT org_ns_fk_df30c182246b TO managed_postgres_databases_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_health: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_health'::regclass AND conname = 'org_ns_fk_aafd96e0959c') THEN
    ALTER TABLE managed_postgres_health DROP CONSTRAINT IF EXISTS managed_postgres_health_account_id_fkey;
    ALTER TABLE managed_postgres_health RENAME CONSTRAINT org_ns_fk_aafd96e0959c TO managed_postgres_health_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_health: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_resizes: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'org_ns_fk_2b4850ad12b7') THEN
    ALTER TABLE managed_postgres_resizes DROP CONSTRAINT IF EXISTS managed_postgres_resizes_account_id_fkey;
    ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT org_ns_fk_2b4850ad12b7 TO managed_postgres_resizes_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_resizes: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_restore_verifications: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_restore_verifications'::regclass AND conname = 'org_ns_fk_b24b26192e79') THEN
    ALTER TABLE managed_postgres_restore_verifications DROP CONSTRAINT IF EXISTS managed_postgres_restore_verifications_account_id_fkey;
    ALTER TABLE managed_postgres_restore_verifications RENAME CONSTRAINT org_ns_fk_b24b26192e79 TO managed_postgres_restore_verifications_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_restore_verifications: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_usage: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage'::regclass AND conname = 'org_ns_fk_535e19141cd6') THEN
    ALTER TABLE managed_postgres_usage DROP CONSTRAINT IF EXISTS managed_postgres_usage_account_id_fkey;
    ALTER TABLE managed_postgres_usage RENAME CONSTRAINT org_ns_fk_535e19141cd6 TO managed_postgres_usage_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_usage: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_usage_imports: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_usage_imports'::regclass AND conname = 'org_ns_fk_0c4aa7699d86') THEN
    ALTER TABLE managed_postgres_usage_imports DROP CONSTRAINT IF EXISTS managed_postgres_usage_imports_account_id_fkey;
    ALTER TABLE managed_postgres_usage_imports RENAME CONSTRAINT org_ns_fk_0c4aa7699d86 TO managed_postgres_usage_imports_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_usage_imports: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases.org_id: SET NOT NULL from the validated check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'managed_postgres_databases'::regclass AND attname = 'org_id' AND NOT attnotnull) THEN
    ALTER TABLE managed_postgres_databases ALTER COLUMN org_id SET NOT NULL;
   END IF;
   ALTER TABLE managed_postgres_databases DROP CONSTRAINT IF EXISTS org_nn_d1a8c4e90b3e;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases.org_id: SET NOT NULL from the validated check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_bindings.database_account_id: SET NOT NULL from the validated check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'managed_postgres_bindings'::regclass AND attname = 'database_account_id' AND NOT attnotnull) THEN
    ALTER TABLE managed_postgres_bindings ALTER COLUMN database_account_id SET NOT NULL;
   END IF;
   ALTER TABLE managed_postgres_bindings DROP CONSTRAINT IF EXISTS org_nn_180774322761;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_bindings.database_account_id: SET NOT NULL from the validated check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- managed_postgres_databases: refuse duplicate live names within an organization
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_databases WHERE state <> 'deleted' GROUP BY org_id, name HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'remediate duplicate managed PostgreSQL names within an organization before migration' USING ERRCODE = '23505';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases_org_name_idx: drop an interrupted build
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('managed_postgres_databases_org_name_idx') AND NOT indisvalid) THEN
    DROP INDEX managed_postgres_databases_org_name_idx;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases_org_name_idx: drop an interrupted build' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- managed_postgres_databases_org_name_idx: build concurrently (waits only for older transactions)
SET lock_timeout = '10s';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS managed_postgres_databases_org_name_idx ON managed_postgres_databases (org_id,name) WHERE state<>'deleted';
SET lock_timeout = '500ms';
-- +goose StatementBegin
-- managed_postgres_databases: name uniqueness moves from the namespace to the organization
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF to_regclass('managed_postgres_databases_org_name_idx') IS NOT NULL THEN
    IF to_regclass('managed_postgres_databases_name_idx') IS NULL
       OR pg_get_indexdef(to_regclass('managed_postgres_databases_name_idx')) LIKE '%(account_id, name)%' THEN
     DROP INDEX IF EXISTS managed_postgres_databases_name_idx;
     ALTER INDEX managed_postgres_databases_org_name_idx RENAME TO managed_postgres_databases_name_idx;
    ELSE
     DROP INDEX managed_postgres_databases_org_name_idx;
    END IF;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases: name uniqueness moves from the namespace to the organization' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- managed_postgres_databases_org_idx: drop an interrupted build
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('managed_postgres_databases_org_idx') AND NOT indisvalid) THEN
    DROP INDEX managed_postgres_databases_org_idx;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: managed_postgres_databases_org_idx: drop an interrupted build' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- managed_postgres_databases_org_idx: build concurrently (waits only for older transactions)
SET lock_timeout = '10s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS managed_postgres_databases_org_idx ON managed_postgres_databases (org_id,id);
SET lock_timeout = '500ms';
RESET lock_timeout;

-- +goose Down
-- Rollback is an operator action on a drained schema, not part of a release:
-- releases roll back by running the previous binaries against this schema.
SET lock_timeout = '5s';
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
RESET lock_timeout;
