-- migration-compat: contract
-- Organization resource lifecycle (ADR-202610171904), applied online
-- (ADR-202610171900, ADR-202610171908). Replaces the single-transaction
-- 20261018003100003, which deadlocked against live writers in rc.277.
--
-- Each step is its own short transaction on at most one existing table
-- (plus the referenced accounts/resource_namespaces row for a foreign key),
-- waits at most 500ms for a lock (below deadlock_timeout, so the
-- migration always yields) and retries. Backfills commit every batch.
-- Constraints are added NOT VALID and validated separately. Every step is
-- idempotent, so an interrupted run resumes, and it converges on databases
-- that applied the original transaction.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '500ms';

-- ---------------------------------------------------------------- 1. new storage namespaces and organization policy columns
-- resource_namespaces: stable storage namespaces (new table)
CREATE TABLE IF NOT EXISTS resource_namespaces (id uuid PRIMARY KEY);
-- +goose StatementBegin
-- orgs: organization policy columns (constant defaults, no rewrite)
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE orgs ADD COLUMN IF NOT EXISTS resource_ownership_enabled boolean NOT NULL DEFAULT false,
       ADD COLUMN IF NOT EXISTS past_due_at timestamptz,
       ADD COLUMN IF NOT EXISTS last_quota_warning_at timestamptz,
       ADD COLUMN IF NOT EXISTS suspended_reason text,
       ADD COLUMN IF NOT EXISTS abuse_hold_at timestamptz,
       ADD COLUMN IF NOT EXISTS abuse_hold_reason text,
       ADD COLUMN IF NOT EXISTS billing_email text NOT NULL DEFAULT '',
       ADD COLUMN IF NOT EXISTS business_name text NOT NULL DEFAULT '',
       ADD COLUMN IF NOT EXISTS billing_address text NOT NULL DEFAULT '',
       ADD COLUMN IF NOT EXISTS tax_id text NOT NULL DEFAULT '';
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: organization policy columns (constant defaults, no rewrite)' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 2. seed namespaces: triggers first, then existing ids in batches
-- seed_resource_namespace
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION seed_resource_namespace() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO resource_namespaces(id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
    RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- accounts: trigger account_resource_namespace
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER account_resource_namespace AFTER INSERT ON accounts
   FOR EACH ROW EXECUTE FUNCTION seed_resource_namespace();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: accounts: trigger account_resource_namespace' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: trigger org_resource_namespace
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_resource_namespace AFTER INSERT ON orgs
   FOR EACH ROW EXECUTE FUNCTION seed_resource_namespace();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_resource_namespace' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- resource_namespaces: seed from existing accounts
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM accounts WHERE true AND id > last ORDER BY id LIMIT 1000) batch;
   IF ids IS NOT NULL THEN
    INSERT INTO resource_namespaces(id) SELECT unnest(ids) ON CONFLICT DO NOTHING;
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
-- +goose StatementBegin
-- resource_namespaces: seed from existing orgs
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM orgs WHERE true AND id > last ORDER BY id LIMIT 1000) batch;
   IF ids IS NOT NULL THEN
    INSERT INTO resource_namespaces(id) SELECT unnest(ids) ON CONFLICT DO NOTHING;
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

-- ---------------------------------------------------------------- 3. functions and views (no table locks)
-- activate_org_resource_ownership
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION activate_org_resource_ownership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE already_enabled boolean:=false;
BEGIN
 IF TG_OP='UPDATE' THEN already_enabled:=OLD.resource_ownership_enabled;END IF;
 NEW.resource_ownership_enabled:=already_enabled OR (NOT NEW.personal_org AND
  (NEW.plan='free' OR EXISTS(SELECT 1 FROM billing_identities b WHERE b.account_id=NEW.id
   AND b.customer_id=NEW.provider_customer_id AND b.subscription_id=NEW.stripe_subscription_item
   AND b.customer_id<>'' AND b.subscription_id<>'')));
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- activate_org_billing_identity
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION activate_org_billing_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.customer_id<>'' AND NEW.subscription_id<>'' THEN
  UPDATE orgs SET resource_ownership_enabled=resource_ownership_enabled,updated_at=now()
  WHERE id=NEW.account_id AND NOT personal_org
   AND provider_customer_id=NEW.customer_id AND stripe_subscription_item=NEW.subscription_id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- app_resource_accounts
-- +goose StatementBegin
-- Resource reads join by app_id: one storage namespace may hold resources from
-- several organizations. Never infer an organization from the namespace alone.
CREATE OR REPLACE VIEW app_resource_accounts AS
SELECT a.id AS app_id, a.account_id AS id,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.past_due_at ELSE ac.past_due_at END)::timestamptz AS past_due_at,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN NULL::timestamptz ELSE ac.deletion_requested_at END)::timestamptz AS deletion_requested_at,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.last_quota_warning_at ELSE ac.last_quota_warning_at END)::timestamptz AS last_quota_warning_at,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.suspended_reason ELSE ac.suspended_reason END)::text AS suspended_reason,
    coalesce((CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.plan ELSE ac.plan END),'')::text AS plan,
    coalesce((CASE WHEN o.personal_org = false AND (o.resource_ownership_enabled OR o.status NOT IN ('active','past_due') OR o.deleted_pending) THEN
        CASE WHEN o.deleted_pending THEN 'deleted_pending' ELSE o.status END
        ELSE ac.status END),'')::text AS status,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.abuse_hold_at ELSE ac.abuse_hold_at END)::timestamptz AS abuse_hold_at,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.abuse_hold_reason ELSE ac.abuse_hold_reason END)::text AS abuse_hold_reason,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN 0 ELSE ac.egress_allowlist_extra END)::integer AS egress_allowlist_extra,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.created_at ELSE ac.created_at END)::timestamptz AS created_at,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.billing_email ELSE ac.email::text END)::text AS email,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.provider_customer_id ELSE ac.provider_customer_id END)::text AS provider_customer_id,
    (CASE WHEN o.personal_org = false AND o.resource_ownership_enabled THEN o.stripe_subscription_item ELSE ac.stripe_subscription_item END)::text AS stripe_subscription_item
FROM apps a LEFT JOIN orgs o ON o.id = a.org_id LEFT JOIN accounts ac ON ac.id = a.account_id;
-- +goose StatementEnd
-- lock_app_resource_account
-- +goose StatementBegin
-- A row lock on the projection above would also lock the nullable creator
-- join. Lock the actual billing owner first, then recheck the app identity.
-- Personal/unadopted apps retain their account fence; adopted apps lock the
-- organization even after the creator identity has been erased.
CREATE OR REPLACE FUNCTION lock_app_resource_account(application_id uuid, exclusive_lock boolean, fail_fast boolean)
RETURNS SETOF app_resource_accounts LANGUAGE plpgsql VOLATILE AS $$
DECLARE target_org uuid; target_namespace uuid; legacy_owner boolean;
BEGIN
 SELECT org_id,account_id INTO target_org,target_namespace FROM apps WHERE id=application_id;
 IF NOT FOUND THEN RETURN; END IF;
 IF exclusive_lock AND fail_fast THEN
  SELECT personal_org OR NOT resource_ownership_enabled INTO legacy_owner FROM orgs WHERE id=target_org FOR UPDATE NOWAIT;
 ELSIF exclusive_lock THEN
  SELECT personal_org OR NOT resource_ownership_enabled INTO legacy_owner FROM orgs WHERE id=target_org FOR UPDATE;
 ELSIF fail_fast THEN
  SELECT personal_org OR NOT resource_ownership_enabled INTO legacy_owner FROM orgs WHERE id=target_org FOR SHARE NOWAIT;
 ELSE
  SELECT personal_org OR NOT resource_ownership_enabled INTO legacy_owner FROM orgs WHERE id=target_org FOR SHARE;
 END IF;
 IF NOT FOUND THEN RETURN; END IF;
 IF legacy_owner THEN
  IF exclusive_lock AND fail_fast THEN
   PERFORM id FROM accounts WHERE id=target_namespace FOR UPDATE NOWAIT;
  ELSIF exclusive_lock THEN
   PERFORM id FROM accounts WHERE id=target_namespace FOR UPDATE;
  ELSIF fail_fast THEN
   PERFORM id FROM accounts WHERE id=target_namespace FOR SHARE NOWAIT;
  ELSE
   PERFORM id FROM accounts WHERE id=target_namespace FOR SHARE;
  END IF;
  IF NOT FOUND THEN RETURN; END IF;
 END IF;
 IF exclusive_lock AND fail_fast THEN
  PERFORM id FROM apps WHERE id=application_id AND org_id=target_org AND account_id=target_namespace FOR UPDATE NOWAIT;
 ELSIF exclusive_lock THEN
  PERFORM id FROM apps WHERE id=application_id AND org_id=target_org AND account_id=target_namespace FOR UPDATE;
 ELSIF fail_fast THEN
  PERFORM id FROM apps WHERE id=application_id AND org_id=target_org AND account_id=target_namespace FOR SHARE NOWAIT;
 ELSE
  PERFORM id FROM apps WHERE id=application_id AND org_id=target_org AND account_id=target_namespace FOR SHARE;
 END IF;
 IF NOT FOUND THEN RETURN; END IF;
 RETURN QUERY SELECT * FROM app_resource_accounts WHERE app_id=application_id;
END $$;
-- +goose StatementEnd
-- org_app_identity_erasure_blocked
-- +goose StatementBegin
-- The reviewed core FK list above is deliberately explicit. Unreviewed
-- account-bound domains below block erasure, including indirect children.
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
-- +goose StatementEnd
-- guard_account_shared_app_purge
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_account_shared_app_purge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF org_app_identity_erasure_blocked(OLD.id) THEN
        RAISE EXCEPTION 'organization ownership or unsupported resource migration required before account purge'
            USING ERRCODE = '23514', CONSTRAINT = 'account_shared_app_ownership_pending';
    END IF;
    RETURN OLD;
END $$;
-- +goose StatementEnd
-- guard_account_shared_app_deletion_transition
-- +goose StatementBegin
-- Alternate transactional writers, including the notification outbox, must
-- enforce the same ownership barrier before changing identity status.
CREATE OR REPLACE FUNCTION guard_account_shared_app_deletion_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM id FROM accounts WHERE id=OLD.id FOR UPDATE;
    IF org_app_identity_erasure_blocked(OLD.id) THEN
        RAISE EXCEPTION 'organization ownership or unsupported resource migration required before account deletion'
            USING ERRCODE='23514', CONSTRAINT='account_shared_app_ownership_pending';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
-- application_standard_egress_guard
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_egress_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'standard egress inputs are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN LATERAL lock_app_resource_account(a.id,false,true) acct ON true
 JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,e NOWAIT;
 PERFORM id FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 PERFORM id FROM instances WHERE app_id=NEW.app_id AND node_id=NEW.node_id
  AND state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining') FOR SHARE NOWAIT;
 actual:=application_standard_egress_target(NEW.app_id,NEW.node_id);
 IF actual IS NULL OR actual<>NEW.target OR actual->>'org_id'<>NEW.org_id::text
  OR actual->'identity'->>'ProtocolVersion'<>'2' OR actual->'identity'->>'Incarnation'=''
  OR NEW.receipt<>jsonb_build_object('identity',actual->'identity','app_id',actual->>'app_id',
   'revision',actual->'policy'->'revision','policy_hash',actual->>'policy_hash') THEN
  RAISE EXCEPTION 'standard egress process or inputs changed' USING ERRCODE='40001',CONSTRAINT='application_standard_egress_current';
 END IF;
 NEW.observed_at:=clock_timestamp();
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- application_standard_egress_target
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_egress_target(app uuid, node uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $_$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,
 'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
 'identity',jsonb_build_object('ProtocolVersion',coalesce(n.vmmd_admission_protocol,0),'NodeID',n.id::text,'Incarnation',coalesce(n.vmmd_incarnation::text,'')),
 'policy',jsonb_build_object('app_id',a.id::text,'revision',a.egress_allowlist_revision,'allowlist',to_jsonb(a.egress_allowlist::text[]),'ports',to_jsonb(a.egress_ports)),
 'policy_hash',application_standard_egress_policy_hash(a))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN app_resource_accounts acct ON acct.app_id=a.id
 JOIN app_application_standards e ON e.app_id=a.id JOIN compute_nodes n ON n.id=node
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND e.effective_hash~'^[a-f0-9]{64}$' AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND n.active AND n.role IS DISTINCT FROM 'control-plane'
 AND EXISTS(SELECT 1 FROM instances i WHERE i.app_id=a.id AND i.node_id=n.id
  AND i.state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining'));
$_$;
-- +goose StatementEnd
-- application_standard_lock_observation_evidence
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_lock_observation_evidence(application_id uuid) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||application_id::text,0)) THEN
  RAISE EXCEPTION 'application standard controls are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN LATERAL lock_app_resource_account(a.id,false,true) acct ON true
 WHERE a.id=application_id FOR SHARE OF o NOWAIT;
 PERFORM p.id FROM projects p JOIN apps a ON a.project_id=p.id WHERE a.id=application_id FOR SHARE OF p NOWAIT;
 PERFORM d.id FROM app_log_drains d WHERE d.app_id=application_id ORDER BY d.id FOR SHARE NOWAIT;
 PERFORM b.app_id FROM application_standard_control_bindings b WHERE b.app_id=application_id
 ORDER BY b.field,b.resource_id FOR SHARE NOWAIT;
 PERFORM r.id FROM application_standard_log_destinations r JOIN application_standard_control_bindings b
 ON b.resource_id=r.id AND b.field='log_destinations' WHERE b.app_id=application_id ORDER BY r.id FOR SHARE OF r NOWAIT;
 PERFORM x.app_id FROM application_standard_log_inventories x WHERE x.app_id=application_id ORDER BY x.node_id FOR SHARE NOWAIT;
 PERFORM h.app_id FROM application_standard_log_health h WHERE h.app_id=application_id ORDER BY h.node_id,h.drain_id FOR SHARE NOWAIT;
 PERFORM e.app_id FROM application_standard_egress_observations e WHERE e.app_id=application_id ORDER BY e.node_id FOR SHARE NOWAIT;
 RETURN true;
END;
$$;
-- +goose StatementEnd
-- application_standard_log_binding
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_log_binding(app uuid, drain uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object('org_id',e.org_id::text,'app_id',a.id::text,'drain_id',d.id::text,
  'resource_id',r.id::text,'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
  'resource_config_hash',r.config_hash,'drain_config_hash',application_standard_log_drain_hash(d))
FROM app_log_drains d
 JOIN apps a ON a.id=d.app_id JOIN orgs o ON o.id=a.org_id JOIN app_resource_accounts acct ON acct.app_id=a.id
 JOIN app_application_standards e ON e.app_id=a.id
 JOIN application_standard_control_bindings b ON b.app_id=a.id AND b.field='log_destinations' AND b.physical_id=d.id::text
 JOIN application_standard_log_destinations r ON r.id=b.resource_id
 WHERE d.app_id=app AND d.id=drain AND d.enabled AND a.status='active' AND d.account_id=a.account_id
 AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND jsonb_array_length(coalesce(e.effective->'sources'->'log_destinations','[]'::jsonb))>0
 AND (coalesce(e.effective->'values'->'log_destinations','[]'::jsonb) ? b.resource_id::text
      OR b.resource_id=ANY(e.additional_log_destinations))
 AND r.org_id=e.org_id;
$$;
-- +goose StatementEnd
-- application_standard_log_health_guard
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_log_health_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'logging health projection is busy' USING ERRCODE='55P03';
 END IF;
 PERFORM h.app_id FROM application_standard_log_health h
  WHERE h.app_id=NEW.app_id AND h.drain_id=NEW.drain_id AND h.node_id=NEW.node_id FOR UPDATE NOWAIT;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN LATERAL lock_app_resource_account(a.id,false,true) acct ON true
  JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,e NOWAIT;
 PERFORM d.id FROM app_log_drains d JOIN application_standard_control_bindings b ON b.app_id=d.app_id AND b.field='log_destinations' AND b.physical_id=d.id::text
  JOIN application_standard_log_destinations r ON r.id=b.resource_id
  WHERE d.app_id=NEW.app_id AND d.id=NEW.drain_id FOR SHARE OF d,b,r NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id FOR SHARE OF c,n NOWAIT;
 IF NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation
   AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'logging health consumer changed' USING ERRCODE='55000';
 END IF;
 IF NEW.status='healthy' THEN
  PERFORM i.id FROM instances i WHERE i.id=NEW.source_instance_id AND i.app_id=NEW.app_id FOR SHARE NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'logging health source changed' USING ERRCODE='40001'; END IF;
 END IF;
 actual:=application_standard_log_binding(NEW.app_id,NEW.drain_id);
 IF actual IS NULL OR actual<>NEW.binding OR actual->>'org_id'<>NEW.org_id::text THEN
  RAISE EXCEPTION 'logging health binding changed' USING ERRCODE='40001';
 END IF;
 NEW.observed_at:=clock_timestamp();
 NEW.event_at:=NEW.observed_at;
 IF TG_OP='UPDATE' THEN
  IF (NEW.app_id,NEW.drain_id,NEW.node_id) IS DISTINCT FROM (OLD.app_id,OLD.drain_id,OLD.node_id) THEN
   RAISE EXCEPTION 'logging health identity changed' USING ERRCODE='40001';
  END IF;
  IF NEW.binding=OLD.binding AND NEW.session_id=OLD.session_id AND NEW.generation=OLD.generation THEN
   IF NEW.event_revision<OLD.event_revision OR (NEW.event_revision=OLD.event_revision AND
    (NEW.status,NEW.reason,NEW.source_instance_id,NEW.sequence) IS DISTINCT FROM (OLD.status,OLD.reason,OLD.source_instance_id,OLD.sequence)) THEN
    RAISE EXCEPTION 'logging health event superseded' USING ERRCODE='GS001';
   END IF;
   IF NEW.event_revision=OLD.event_revision THEN NEW.event_at:=OLD.event_at; END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- application_standard_log_inventory
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_log_inventory(app uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,'account_id',a.account_id::text,
  'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,'drains',
  coalesce((SELECT jsonb_agg(jsonb_build_object('drain_id',d.id::text,'config_hash',application_standard_log_drain_hash(d)) ORDER BY d.id)
   FROM app_log_drains d WHERE d.app_id=a.id AND d.enabled),'[]'::jsonb))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN app_resource_accounts acct ON acct.app_id=a.id
 JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp());
$$;
-- +goose StatementEnd
-- application_standard_log_inventory_guard
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_log_inventory_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 -- This nonwaiting exclusive fence also covers drain inserts/deletes, not just existing rows.
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'logging inventory is busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN LATERAL lock_app_resource_account(a.id,false,true) acct ON true
 JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=NEW.app_id FOR SHARE OF a,o,e NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
 WHERE c.node_id=NEW.node_id FOR SHARE OF c,n NOWAIT;
 NEW.observed_at:=clock_timestamp();
 actual:=application_standard_log_inventory(NEW.app_id);
 IF actual IS NULL OR actual<>NEW.inventory OR actual->>'org_id'<>NEW.org_id::text THEN
  RAISE EXCEPTION 'application standard logging inventory changed' USING ERRCODE='40001';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation
   AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'application standard logging consumer changed' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- capture_instance_capacity
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.capture_instance_capacity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE p service_capacity_policy%ROWTYPE; main_cpu integer; guest_vcpu integer; side_ram bigint; side_cpu bigint;
BEGIN
    IF TG_OP='UPDATE' THEN
        NEW.capacity_ram_mb:=greatest(OLD.capacity_ram_mb,NEW.capacity_ram_mb);
        NEW.capacity_cpu_millicores:=greatest(OLD.capacity_cpu_millicores,NEW.capacity_cpu_millicores);
        NEW.capacity_vcpu:=greatest(OLD.capacity_vcpu,NEW.capacity_vcpu);
        RETURN NEW;
    END IF;
    SELECT * INTO STRICT p FROM service_capacity_policy WHERE singleton;
    SELECT greatest(a.cpu_millicores,p.startup_cpu),coalesce((p.plan_vcpus->>ac.plan)::integer,4)
      INTO main_cpu,guest_vcpu FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id WHERE a.id=NEW.app_id;
    SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0),
           coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
      INTO side_ram,side_cpu FROM deployments d,
        LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=NEW.deployment_id;
    NEW.capacity_ram_mb:=greatest(NEW.capacity_ram_mb,NEW.ram_mb+p.overhead_mb+side_ram);
    NEW.capacity_cpu_millicores:=greatest(NEW.capacity_cpu_millicores,coalesce(main_cpu,p.startup_cpu)+side_cpu);
    NEW.capacity_vcpu:=greatest(NEW.capacity_vcpu,coalesce(guest_vcpu,1));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- guard_environment_workload_instance
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_environment_workload_instance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; app_ram integer; may_deploy boolean; b environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM environment_qualification_executions WHERE instance_id=NEW.id) THEN
  RAISE EXCEPTION 'retained qualification instance cannot be reused' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments d
   JOIN environment_git_sources s ON s.id=(d.environment_workload_runtime->>'source_id')::uuid
   JOIN project_environments e ON e.id=(d.environment_workload_runtime->>'environment_id')::uuid WHERE d.id=OLD.deployment_id) THEN
   SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=OLD.id OR id=(SELECT request_id FROM environment_qualification_restore_reservations WHERE instance_id=OLD.id) FOR UPDATE;
   IF OLD.state NOT IN ('parked','stopped','failed') OR (q.id IS NOT NULL AND q.lease_until>clock_timestamp()) THEN
    RAISE EXCEPTION 'environment qualification reservation must remain until retired and its attempt released' USING ERRCODE='23514';
   END IF;
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL)
  AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.ram_mb,NEW.kind,NEW.job_id)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.ram_mb,OLD.kind,OLD.job_id) THEN
  RAISE EXCEPTION 'environment qualification instance identity is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=NEW.id;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id OR
  (b.instance_id IS NOT NULL AND id=b.request_id AND reserved_instance_id=b.capture_instance_id AND attempt=b.attempt);
 IF q.id IS NOT NULL AND (q.app_id IS DISTINCT FROM NEW.app_id OR q.deployment_id IS DISTINCT FROM NEW.deployment_id) THEN
  RAISE EXCEPTION 'environment qualification reservation cannot be reassigned' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND q.id IS NULL AND NEW.state IN ('draining','snapshotting','parked','stopped','failed','evicting_account_deleting') AND
  EXISTS(SELECT 1 FROM environment_workload_retirements r JOIN deployments d ON d.id=OLD.deployment_id
   WHERE d.id=ANY(r.deployment_ids) AND d.status='superseded' AND NOT d.environment_workload_held AND r.phase NOT IN ('planned','cancelled')) THEN RETURN NEW; END IF;
 IF q.id IS NULL AND EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND app_id=NEW.app_id
  AND environment_workload_runtime IS NOT NULL AND NOT environment_workload_held AND status='live') THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment qualification instances must be created under their attempt' USING ERRCODE='23514';
 END IF;
 -- Retirement must remain available after expiry, supersession and parent
 -- deletion. It cannot grant execution or qualification evidence.
 IF TG_OP='UPDATE' AND NEW.state IN ('parked','stopped','failed','evicting_account_deleting') THEN RETURN NEW; END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id
   WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=q.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR (q.execution_mode='job' AND
    (coalesce(jsonb_typeof(q.frozen_inputs->'job_smoke'),'null')<>'object' OR
     coalesce(nullif(q.frozen_inputs->'queue_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb
     OR NOT EXISTS(SELECT 1 FROM environment_workload_graphs g, jsonb_array_elements(g.members) member
       WHERE g.id=q.graph_id AND member->>'resource'=q.resource AND member->>'execution_mode'='job'
        AND coalesce((member->>'job_smoke_configured')::boolean,false)
        AND coalesce(nullif(member->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)
        AND coalesce((member->>'service_bindings_configured')::boolean,false)=(coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb)
        AND coalesce(member->>'queue_bindings_configured','false')='false'
        AND coalesce(nullif(member->'queue_bindings','null'::jsonb),'{}'::jsonb)='{}'::jsonb
        AND coalesce(nullif(member->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb))) OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>NEW.app_id OR NEW.kind<>'wake' OR NEW.job_id IS NOT NULL
  OR NEW.mode IS DISTINCT FROM (CASE q.execution_mode WHEN 'worker' THEN 'worker' WHEN 'service' THEN 'service' ELSE 'normal' END)
  OR (b.instance_id IS NOT NULL AND (NOT environment_qualification_restore_current(q.id,b.capture_instance_id)
   OR NEW.wake_id=(SELECT (frame->>'wake_id')::uuid FROM environment_qualification_executions WHERE instance_id=b.capture_instance_id)))
  OR NOT environment_workload_qualification_inputs_current(q.id) THEN
  RAISE EXCEPTION 'environment workload execution requires its current qualification attempt' USING ERRCODE='23514';
 END IF;
 SELECT a.ram_mb,c.status='active' AND c.abuse_hold_at IS NULL INTO app_ram,may_deploy
  FROM apps a JOIN app_resource_accounts c ON c.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a;
 IF NOT coalesce(may_deploy,false) OR NEW.ram_mb IS DISTINCT FROM app_ram OR NOT EXISTS(
  SELECT 1 FROM compute_nodes WHERE id=NEW.node_id AND active AND lifecycle='active') OR
  (TG_OP='INSERT' AND (NEW.state<>'cold_booting' OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL
   OR coalesce(NEW.guest_uid,0)<>0 OR NEW.framework_ready_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'environment qualification admission shape is invalid' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- guard_environment_workload_intent
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_environment_workload_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
 row_value jsonb; prior jsonb; src environment_git_sources%ROWTYPE;
 resource_name text; paths text[]; controller boolean;
BEGIN
 IF TG_OP='DELETE' THEN row_value:=to_jsonb(OLD); ELSE row_value:=to_jsonb(NEW); END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM apps a JOIN app_resource_accounts c ON c.app_id=a.id
  JOIN project_environments e ON e.account_id=a.account_id AND e.project_id=a.project_id
  WHERE a.id=OLD.app_id AND a.account_id=OLD.account_id AND e.id=OLD.environment_id) THEN
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.environment_id IS DISTINCT FROM OLD.environment_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_workload_intent_identity',MESSAGE='scoped workload identity is immutable';
 END IF;
 IF TG_OP<>'DELETE' AND NOT EXISTS(SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
  WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id AND e.id=NEW.environment_id AND a.status<>'deleted') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_workload_intent_identity',MESSAGE='scoped workload requires its original project environment';
 END IF;
 SELECT s.* INTO src FROM active_environment_git_sources s WHERE s.account_id=(row_value->>'account_id')::uuid
  AND s.environment_id=(row_value->>'environment_id')::uuid FOR UPDATE OF s;
 IF TG_OP='INSERT' THEN
  SELECT to_jsonb(w) INTO prior FROM app_environment_workload_intents w
   WHERE w.app_id=NEW.app_id AND w.environment_id=NEW.environment_id FOR UPDATE OF w;
  prior:=coalesce(prior,'{}');
 ELSE prior:=to_jsonb(OLD); END IF;
 IF TG_OP='DELETE' THEN row_value:=row_value||jsonb_build_object('source',NULL,'source_revision',NULL,'runtime','{}'::jsonb,'service_bindings','{}'::jsonb,'schedule',NULL,'variables','{}'::jsonb); END IF;
 SELECT array_agg('runtime/'||k) INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'runtime','{}')) UNION SELECT key FROM jsonb_each(row_value->'runtime')
 ) keys WHERE (prior->'runtime'->k) IS DISTINCT FROM (row_value->'runtime'->k);
 IF coalesce(prior->'source','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source']; END IF;
 IF coalesce(prior->'source_revision','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source_revision','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source_revision','source']; END IF;
 SELECT coalesce(paths,'{}')||coalesce(array_agg('service_bindings/'||k),'{}') INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'service_bindings','{}')) UNION SELECT key FROM jsonb_each(row_value->'service_bindings')
 ) keys WHERE (prior->'service_bindings'->k) IS DISTINCT FROM (row_value->'service_bindings'->k);
 IF prior->'schedule' IS DISTINCT FROM row_value->'schedule' THEN paths:=coalesce(paths,'{}')||ARRAY['schedule']; END IF;
 SELECT coalesce(paths,'{}')||coalesce(array_agg('variables/'||k),'{}') INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'variables','{}')) UNION SELECT key FROM jsonb_each(row_value->'variables')
 ) keys WHERE (prior->'variables'->k) IS DISTINCT FROM (row_value->'variables'->k);
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.service_bindings) b WHERE
  b.key !~ '^[a-z0-9][a-z0-9-]*$' OR jsonb_typeof(b.value)<>'object' OR
  NOT b.value ?& ARRAY['workload','env_key','target_app_id'] OR b.value->>'env_key' !~ '^[A-Za-z_][A-Za-z0-9_]*$' OR
  NOT EXISTS(SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id
   WHERE r.source_id=src.id AND r.logical_name='workload/'||(b.value->>'workload') AND a.id=(b.value->>'target_app_id')::uuid
   AND a.id<>NEW.app_id AND a.account_id=NEW.account_id AND a.project_id=src.project_id AND a.status<>'deleted')) THEN
  RAISE EXCEPTION 'scoped service binding requires its original environment target' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.variables) v WHERE
  v.key !~ '^[A-Z][A-Z0-9_]*$' OR length(v.key)>128 OR jsonb_typeof(v.value)<>'string' OR
  octet_length(v.value #>> '{}')>32768) THEN
  RAISE EXCEPTION 'scoped workload variables require valid keys and string values' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND (SELECT count(*) FROM jsonb_object_keys(NEW.variables))>256 THEN
  RAISE EXCEPTION 'scoped workload variable count exceeds the platform limit' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND ((SELECT count(*) FROM jsonb_object_keys(NEW.service_bindings))>100 OR EXISTS(
  SELECT 1 FROM jsonb_each(NEW.service_bindings) GROUP BY value->>'env_key' HAVING count(*)>1)) THEN
  RAISE EXCEPTION 'scoped service binding count or environment keys are invalid' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND NEW.source->>'kind'='function' AND NOT EXISTS(SELECT 1 FROM apps a
  WHERE a.id=NEW.app_id AND a.type='function' AND a.runtime=NEW.source->>'runtime') THEN
  RAISE EXCEPTION 'function source requires its original supported runner' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND NEW.schedule IS NOT NULL AND NOT EXISTS(SELECT 1 FROM apps a WHERE a.id=NEW.app_id
  AND (NEW.runtime->>'execution_mode'='job' OR a.manifest->>'execution_mode'='job')) THEN
  RAISE EXCEPTION 'scheduled workload requires job execution mode' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.service_bindings) b
  JOIN project_environments e ON e.id=NEW.environment_id WHERE
   EXISTS(SELECT 1 FROM app_envs v WHERE v.app_id=NEW.app_id AND v.scope=e.slug AND v.key=b.value->>'env_key') OR
   EXISTS(SELECT 1 FROM app_environment_secret_refs r WHERE r.app_id=NEW.app_id AND r.environment_id=e.id AND r.key=b.value->>'env_key') OR
   NEW.variables ? (b.value->>'env_key')) THEN
  RAISE EXCEPTION 'scoped service binding environment key is already occupied' USING ERRCODE='23514';
 END IF;
 IF coalesce(cardinality(paths),0)=0 THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=(row_value->>'app_id')::uuid;
  controller:=EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true) AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id
   AND f.environment_id=src.environment_id AND f.resource=resource_name AND f.field_path=ANY(paths)
   AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=f.environment_id
    AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at); END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$_$;
-- +goose StatementEnd
-- lifecycle_configuration
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.lifecycle_configuration(app uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object('app',jsonb_build_object('id',a.id,'account',a.account_id,'slug',a.slug,
 'consumer_auth_mode',a.consumer_auth_mode,'maintenance_mode',a.maintenance_mode,
 'manifest',a.manifest,'ram_mb',a.ram_mb,'max_concurrency',a.max_concurrency,'idle_timeout_s',a.idle_timeout_s,'type',a.type,'cpu_millicores',a.cpu_millicores,'scaling_policy',a.scaling_policy,'request_rate_limit_rps',a.request_rate_limit_rps,'request_rate_limit_burst',a.request_rate_limit_burst),
 'account',jsonb_build_object('plan',c.plan,'status',c.status,'past_due_at',c.past_due_at,'abuse_hold_at',c.abuse_hold_at),
 'rules',coalesce((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM edge_rules r WHERE r.app_id=a.id),'[]'::jsonb))
 FROM apps a JOIN app_resource_accounts c ON c.app_id=a.id WHERE a.id=app
$$;
-- +goose StatementEnd
-- service_capacity_snapshot
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.service_capacity_snapshot() RETURNS jsonb
    LANGUAGE sql
    SET jit TO 'off'
    AS $$
WITH policy AS (SELECT * FROM service_capacity_policy WHERE singleton),
eligible AS (
    SELECT a.id, a.ram_mb, a.cpu_millicores, a.manifest,
           coalesce((p.plan_vcpus->>ac.plan)::integer, 4) AS vcpu
      FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id CROSS JOIN policy p
     WHERE a.status IN ('active','evicted_cold') AND a.manifest->>'execution_mode'='service'
), scopes AS (
    SELECT id AS app_id, 'default'::text AS scope FROM eligible
    UNION
    SELECT d.app_id, d.scope FROM deployments d JOIN eligible a ON a.id=d.app_id
     WHERE d.status IN ('pending','building','imaging','snapshotting','live')
), declaration AS (
    SELECT s.app_id::text || ':' || s.scope AS key,
           greatest(0, coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) AS desired,
           CASE WHEN EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')
                  AND (EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status IN ('pending','building','imaging','snapshotting'))
                    OR (SELECT count(*) FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')>1)
                THEN 1 ELSE 0 END AS surge,
           a.ram_mb+p.overhead_mb+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS ram,
           greatest(a.cpu_millicores,p.startup_cpu)+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS cpu,
           a.vcpu
      FROM scopes s JOIN eligible a ON a.id=s.app_id CROSS JOIN policy p
), demand AS (
    SELECT key, CASE WHEN desired=0 THEN 0 ELSE desired+surge END AS count, desired, ram, cpu, vcpu
      FROM declaration
), resident AS (
    SELECT i.node_id, a.id::text || ':' || coalesce(d.scope,'default') AS key,
           greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb+coalesce((SELECT sum(greatest(0,(sc->>'ram_mb')::integer))
                           FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS ram,
           greatest(i.capacity_cpu_millicores,greatest(coalesce(a.cpu_millicores,p.startup_cpu),p.startup_cpu)+coalesce((
               SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu)))
                 FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS cpu,
           greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END) AS vcpu,
           (i.state<>'warm' AND i.mode IN ('normal','service') AND a.manifest->>'execution_mode'='service'
              AND EXISTS (SELECT 1 FROM demand x WHERE x.key=a.id::text || ':' || coalesce(d.scope,'default') )) AS service
      FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN app_resource_accounts ac ON ac.app_id=a.id
      LEFT JOIN deployments d ON d.id=i.deployment_id CROSS JOIN policy p
     WHERE i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating')
    UNION ALL
    SELECT r.destination_node_id,NULL::text,r.ram_mb::bigint,r.cpu_millicores::bigint,r.vcpu::bigint,false
      FROM migration_admissions r WHERE r.phase NOT IN ('cleaned','committed')
       AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=r.instance_id AND i.node_id=r.destination_node_id AND i.wake_id=r.destination_wake_id AND i.state='running')
    UNION ALL
    SELECT n.id,NULL::text,u.ram,u.cpu,u.vcpu,false FROM compute_nodes n CROSS JOIN LATERAL migration_source_node_usage(n.id) u
      WHERE u.ram>0 OR u.cpu>0 OR u.vcpu>0
), shape AS (
    SELECT greatest(coalesce((SELECT max(ram) FROM demand WHERE count>0),0),coalesce((SELECT max(ram) FROM resident WHERE service),0),1) AS ram,
           greatest(coalesce((SELECT max(cpu) FROM demand WHERE count>0),0),coalesce((SELECT max(cpu) FROM resident WHERE service),0),1) AS cpu,
           greatest(coalesce((SELECT max(vcpu) FROM demand WHERE count>0),0),coalesce((SELECT max(vcpu) FROM resident WHERE service),0),1) AS vcpu
), other_usage AS (
    SELECT node_id, sum(ram)::bigint AS ram, sum(cpu)::bigint AS cpu, sum(vcpu)::bigint AS vcpu
      FROM resident WHERE NOT coalesce(service,false) GROUP BY node_id
), service_usage AS (
    SELECT node_id, count(*)::bigint AS count FROM resident WHERE service GROUP BY node_id
), nodes AS (
    SELECT n.id, n.name, coalesce(u.ram,0) AS other_ram, coalesce(u.cpu,0) AS other_cpu, coalesce(u.vcpu,0) AS other_vcpu,
           coalesce(s.count,0) AS service_count,
           (coalesce(u.ram,0)<=n.admission_ceiling_mb AND coalesce(u.cpu,0)<=n.vpcpus::bigint*1000*p.cpu_overcommit AND coalesce(u.vcpu,0)<=n.vcpu_budget) AS ordinary_fit,
           greatest(0,least((n.admission_ceiling_mb-coalesce(u.ram,0))/sh.ram,
                       (n.vpcpus::bigint*1000*p.cpu_overcommit-coalesce(u.cpu,0))/sh.cpu,
                       (n.vcpu_budget-coalesce(u.vcpu,0))/sh.vcpu))::bigint AS slots
      FROM compute_nodes n CROSS JOIN policy p CROSS JOIN shape sh
      LEFT JOIN other_usage u ON u.node_id=n.id LEFT JOIN service_usage s ON s.node_id=n.id
     WHERE n.lifecycle='active' AND n.admission_ceiling_mb>0 AND n.vpcpus>0 AND n.vcpu_budget>0
       AND n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>p.heartbeat_seconds)
), totals AS (
    SELECT count(*) AS healthy_nodes, coalesce(sum(slots),0) AS fleet_slots,
           coalesce(sum(slots)-max(slots),0) AS failover_slots,
           coalesce(bool_and(service_count<=slots AND ordinary_fit),true) AS placements_fit FROM nodes
), required AS (SELECT coalesce(sum(count),0) AS count, coalesce(sum(desired),0) AS desired FROM demand),
actual_targets AS (SELECT key,count(*) AS count FROM resident WHERE service GROUP BY key),
projection AS (
    SELECT jsonb_build_object(
        'enabled',p.enabled,
        'state',CASE WHEN NOT p.enabled THEN 'disabled'
                     WHEN t.healthy_nodes>=2 AND t.placements_fit AND r.count<=t.failover_slots
                       AND NOT EXISTS (SELECT 1 FROM actual_targets a JOIN demand d ON d.key=a.key WHERE a.count>d.count) THEN 'protected'
                     WHEN t.placements_fit AND r.count<=t.fleet_slots THEN 'degraded' ELSE 'needs_hardware' END,
        'healthy_nodes',t.healthy_nodes,'desired_replicas',r.desired,'reserved_replicas',r.count,
        'replica_ram_mb',sh.ram,'replica_cpu_millicores',sh.cpu,'replica_vcpu',sh.vcpu,
        'fleet_slots',t.fleet_slots,'failover_slots',t.failover_slots,'placements_fit',t.placements_fit,
        'demands',coalesce((SELECT jsonb_object_agg(key,jsonb_build_object('count',count,'ram',ram,'cpu',cpu,'vcpu',vcpu)) FROM demand),'{}'::jsonb),
        'other',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('ram',other_ram,'cpu',other_cpu,'vcpu',other_vcpu)) FROM nodes),'{}'::jsonb),
        'placement',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('slots',slots,'used',service_count)) FROM nodes),'{}'::jsonb),
        'service_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM service_usage),'{}'::jsonb),
        'resident',coalesce((SELECT jsonb_object_agg(node_id::text,cost) FROM (SELECT node_id,jsonb_build_object('ram',sum(ram),'cpu',sum(cpu),'vcpu',sum(vcpu)) AS cost FROM resident GROUP BY node_id) u),'{}'::jsonb),
        'actual',coalesce((SELECT jsonb_object_agg(key,count) FROM actual_targets),'{}'::jsonb)
    ) AS data FROM policy p CROSS JOIN totals t CROSS JOIN required r CROSS JOIN shape sh
) SELECT data FROM projection;
$$;
-- +goose StatementEnd
-- workflow_event_recipients
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.workflow_event_recipients(target_account uuid, event_source text, event_type text) RETURNS TABLE(recipient jsonb)
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(app_workflow_definitions(a.id,d.workflows)) definition
 WHERE a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;
-- +goose StatementEnd
-- workflow_tenant_event_recipients
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.workflow_tenant_event_recipients(target_account uuid, target_app uuid, target_tenant uuid, event_source text, event_type text) RETURNS TABLE(recipient jsonb)
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.tenant-event:' || a.id::text || ':' || target_tenant::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'platform_tenant_id', target_tenant,
   'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id
 JOIN platform_tenants t ON t.id = target_tenant AND t.account_id = a.account_id AND t.status = 'active'
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(app_workflow_definitions(a.id, d.workflows)) definition
 WHERE a.id = target_app AND a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND (
     EXISTS (SELECT 1 FROM api_consumers c WHERE c.account_id = a.account_id AND c.app_id = a.id
       AND c.platform_tenant_id = t.id AND c.status = 'active' AND c.revoked_at IS NULL)
     OR EXISTS (SELECT 1 FROM tenant_surfaces s WHERE s.account_id = a.account_id AND s.app_id = a.id
       AND s.platform_tenant_id = t.id AND s.status = 'active')
   )
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;
-- +goose StatementEnd
-- apps_streaming_owner_allowed
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION apps_streaming_owner_allowed(namespace_id uuid, organization_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN o.personal_org=false AND o.resource_ownership_enabled THEN o.plan<>'free'
        ELSE EXISTS(SELECT 1 FROM accounts a WHERE a.id=namespace_id AND a.plan<>'free') END
 FROM orgs o WHERE o.id=organization_id
 UNION ALL
 SELECT EXISTS(SELECT 1 FROM accounts a WHERE a.id=namespace_id AND a.plan<>'free')
 WHERE organization_id IS NULL;
$$;
-- +goose StatementEnd
-- apps_streaming_plan_app_guard
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION apps_streaming_plan_app_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.streaming_enabled THEN
  IF EXISTS(SELECT 1 FROM orgs WHERE id=NEW.org_id AND NOT personal_org AND resource_ownership_enabled) THEN
   PERFORM id FROM orgs WHERE id=NEW.org_id FOR SHARE;
  ELSE
   PERFORM id FROM accounts WHERE id=NEW.account_id FOR NO KEY UPDATE;
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- apps_streaming_plan_account_guard
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION apps_streaming_plan_account_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.plan='free' AND OLD.plan IS DISTINCT FROM NEW.plan AND EXISTS(
  SELECT 1 FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.account_id=NEW.id
  AND (a.org_id IS NULL OR o.personal_org OR NOT o.resource_ownership_enabled) AND a.streaming_enabled) THEN
  RAISE EXCEPTION 'personal streaming app prevents Free downgrade' USING ERRCODE='23514',CONSTRAINT='apps_streaming_enabled_plan_check';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- org_streaming_plan_guard
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION org_streaming_plan_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT NEW.personal_org AND NEW.plan='free' AND OLD.plan IS DISTINCT FROM NEW.plan
  AND EXISTS(SELECT 1 FROM apps WHERE org_id=NEW.id AND streaming_enabled) THEN
  RAISE EXCEPTION 'company streaming app prevents Free downgrade' USING ERRCODE='23514',CONSTRAINT='apps_streaming_enabled_plan_check';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- org_owner_identity_active
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION org_owner_identity_active() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- billing_resource_accounts
-- +goose StatementBegin
CREATE OR REPLACE VIEW billing_resource_accounts AS
 SELECT id,plan,status,abuse_hold_at FROM accounts
 UNION ALL
 SELECT id,plan,CASE WHEN deleted_pending THEN 'deleted_pending' ELSE status END,abuse_hold_at FROM orgs WHERE NOT personal_org;
-- +goose StatementEnd
-- retain_financial_usage_evidence
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.retain_financial_usage_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  previous_compute bigint := 0;
  previous_egress bigint := 0;
  captured_plan text;
  captured_attribution jsonb;
  delta bigint;
  meter_name text;
  unit_name text;
  cumulative bigint;
  captured_price text;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    previous_compute := OLD.mb_seconds;
    previous_egress := OLD.net_tx_bytes;
  END IF;
  IF NEW.mb_seconds = previous_compute AND NEW.net_tx_bytes = previous_egress THEN RETURN NEW; END IF;
  IF NEW.mb_seconds < previous_compute OR NEW.net_tx_bytes < previous_egress THEN
    RAISE EXCEPTION 'financial usage corrections require retained adjustment lineage';
  END IF;
  -- Allocate evidence IDs in account commit order. Without this transaction
  -- lock, a lower sequence could commit after a paged reader fixed its head.
  PERFORM pg_advisory_xact_lock(hashtextextended('financial-evidence:' || NEW.account_id::text, 0));

  -- Preserve the first observation's identity even if the live workload is
  -- renamed, moved or deleted before later network observations arrive.
  SELECT plan, attribution INTO captured_plan, captured_attribution
    FROM financial_usage_evidence WHERE account_id = NEW.account_id
      AND instance_id = NEW.instance_id AND source_start = NEW.minute
    ORDER BY id LIMIT 1;
  IF NOT FOUND THEN
    -- Recorded activations apply to source intervals. In particular, the
    -- just-closed minute still uses its contract when the live plan changes.
    SELECT COALESCE((
      SELECT price.plan FROM financial_price_snapshots price
      WHERE price.account_id = NEW.account_id AND price.meter = 'compute'
        AND price.period_start <= NEW.minute AND price.period_end > NEW.minute
        AND price.effective_from <= NEW.minute
      ORDER BY price.effective_from DESC, price.recorded_at DESC, price.version
      LIMIT 1
    ), account.plan), jsonb_strip_nulls(jsonb_build_object(
      'app_id', NEW.app_id::text, 'job_id', NEW.job_id::text,
      'project_id', app.project_id::text, 'deployment_id', instance.deployment_id::text,
      'environment_id', environment.id::text, 'name', COALESCE(app.slug, job.name)))
    INTO captured_plan, captured_attribution
    FROM billing_resource_accounts account
    LEFT JOIN apps app ON app.id = NEW.app_id AND (app.account_id = account.id OR app.org_id = account.id)
    LEFT JOIN jobs job ON job.id = NEW.job_id AND job.account_id = account.id
    LEFT JOIN instances instance ON instance.id = NEW.instance_id AND instance.app_id = app.id
    LEFT JOIN deployments deployment ON deployment.id = instance.deployment_id AND deployment.app_id = app.id
    LEFT JOIN project_environments environment ON environment.account_id = account.id
      AND environment.project_id = app.project_id AND environment.slug = deployment.scope
    WHERE account.id = NEW.account_id;
    -- Preserve canonical usage behavior for orphan fixtures and late samples
    -- after account deletion without recreating erased financial history.
    IF NOT FOUND THEN RETURN NEW; END IF;
  END IF;
  FOR meter_name, unit_name, delta, cumulative IN
    SELECT 'compute', 'mb_seconds', NEW.mb_seconds - previous_compute, NEW.mb_seconds
    UNION ALL SELECT 'egress', 'interface_bytes', NEW.net_tx_bytes - previous_egress, NEW.net_tx_bytes
  LOOP
    IF delta > 0 THEN
      SELECT version INTO captured_price FROM financial_price_snapshots
        WHERE account_id = NEW.account_id AND meter = meter_name AND plan = captured_plan
          AND period_start <= NEW.minute AND period_end > NEW.minute AND effective_from <= NEW.minute
        ORDER BY effective_from DESC, recorded_at DESC, version LIMIT 1;
      INSERT INTO financial_usage_evidence(account_id, instance_id, source_id, meter, unit, quantity,
        source_start, source_end, plan, attribution, price_version)
      VALUES (NEW.account_id, NEW.instance_id,
        'usage:' || NEW.instance_id::text || ':' || extract(epoch FROM NEW.minute)::text || ':' || meter_name || ':' || cumulative::text,
        meter_name, unit_name, delta, NEW.minute, NEW.minute + interval '1 minute', captured_plan, captured_attribution, captured_price);
    END IF;
  END LOOP;
  RETURN NEW;
END $$;
-- +goose StatementEnd
-- bind_app_builder_owner
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_app_builder_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_org uuid;
BEGIN
 SELECT o.id INTO app_org FROM apps a JOIN orgs o ON o.id=a.org_id WHERE a.id=NEW.app_id AND NOT o.personal_org AND o.resource_ownership_enabled;
 IF app_org IS NOT NULL THEN NEW.account_id:=app_org;NEW.org_id:=app_org;END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- bind_app_storage_owner
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_app_storage_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_org uuid; original_subject uuid;
BEGIN
 SELECT o.id INTO app_org FROM apps a JOIN orgs o ON o.id=a.org_id WHERE a.id=NEW.app_id AND NOT o.personal_org AND o.resource_ownership_enabled;
 IF app_org IS NOT NULL THEN
  SELECT account_id INTO original_subject FROM snapshot_storage_daily WHERE app_id=NEW.app_id AND day=NEW.day ORDER BY account_id LIMIT 1;
  NEW.account_id:=coalesce(original_subject,app_org);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- bind_core_app_resource_org
-- +goose StatementBegin
-- Denormalized organization FKs follow the app root, including repaired legacy
-- personal attribution. Billing subject UUIDs and quantities remain untouched.
CREATE OR REPLACE FUNCTION bind_core_app_resource_org() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owning_org uuid;
BEGIN
 SELECT org_id INTO owning_org FROM apps WHERE id=NEW.app_id;
 IF owning_org IS NOT NULL THEN NEW.org_id:=owning_org;END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- application_standard_runtime_snapshot
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_runtime_snapshot(application_id uuid, artifact_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE a apps%ROWTYPE; e app_application_standards%ROWTYPE;
        acct app_resource_accounts%ROWTYPE; o orgs%ROWTYPE; d deployments%ROWTYPE; producers jsonb;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime application is missing' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 SELECT * INTO o FROM orgs WHERE id=a.org_id FOR SHARE NOWAIT;
 IF o.personal_org OR a.org_id IS NULL THEN
  PERFORM id FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 END IF;
 SELECT * INTO acct FROM app_resource_accounts WHERE app_id=a.id;
 SELECT * INTO e FROM app_application_standards WHERE app_id=a.id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.status='deleted' OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL OR o.status NOT IN ('active','past_due') OR o.deleted_pending
   OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR (e.exception_expires_at IS NOT NULL AND e.exception_expires_at<=clock_timestamp())
   OR NOT ((e.state='unmanaged' AND e.persisted_revision=0 AND e.adoptions='[]'::jsonb AND cardinality(e.materialized_fields)=0)
     OR (e.state IN ('persisted','observed') AND e.persisted_revision=e.desired_revision AND e.effective_hash <> '')) THEN
  RAISE EXCEPTION 'runtime application standards are incomplete' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0)) THEN
  RAISE EXCEPTION 'runtime controls are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 IF artifact_id IS NOT NULL THEN
  SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
  IF NOT FOUND OR d.app_id IS DISTINCT FROM a.id THEN
   RAISE EXCEPTION 'runtime artifact belongs to another application' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
  END IF;
  IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
   RAISE EXCEPTION 'runtime artifact children are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
  END IF;
 END IF;
 IF artifact_id IS NOT NULL THEN producers:=application_standard_runtime_producers(a,d); END IF;
 RETURN application_standard_stable_runtime_input(jsonb_build_object(
  'app_id',a.id::text,'org_id',a.org_id::text,'project_id',coalesce(a.project_id::text,''),'account_id',a.account_id::text,
  'account_plan',acct.plan,'account_egress_allowlist_extra',acct.egress_allowlist_extra,
  'desired_revision',e.desired_revision,'persisted_revision',e.persisted_revision,'effective_hash',e.effective_hash,
  'adoptions',e.adoptions,'materialized_fields',to_jsonb(e.materialized_fields),'effective',e.effective,
  'base_settings',e.base_settings,'local_settings',e.local_settings,'additional_log_destinations',to_jsonb(e.additional_log_destinations::text[]),
  'settings',jsonb_build_object('require_signed',a.require_signed,'security_policy',a.security_policy,
    'egress_cidrs',to_jsonb(a.egress_allowlist::text[]),'egress_extra_ports',to_jsonb(a.egress_ports)),
  'runtime',jsonb_build_object('type',a.type,'runtime',coalesce(a.runtime,''),'ram_mb',a.ram_mb,'app_protocol',a.app_protocol),
  'drains',coalesce((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'kind',r.kind,'enabled',r.enabled,
    'target_hash',encode(sha256(convert_to(r.target_url,'UTF8')),'hex'),
    'auth_hash',encode(sha256(coalesce(r.auth_header_sealed,''::bytea)),'hex')) ORDER BY r.id)
    FROM app_log_drains r WHERE r.app_id=a.id),'[]'::jsonb),
  'signers',coalesce((SELECT jsonb_agg(jsonb_build_object('name',r.signer_name,
    'fingerprint',encode(sha256(r.cosign_public_key),'hex')) ORDER BY r.signer_name)
    FROM app_trusted_signers r WHERE r.app_id=a.id),'[]'::jsonb),
  'artifact',CASE WHEN artifact_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object(
    'id',d.id::text,'scope',d.scope,'kind',d.kind,'image_digest',coalesce(d.image_digest,''),
    'rootfs_key',coalesce(d.rootfs_key,''),'rootfs_path',coalesce(d.rootfs_path,''),'rootfs_bytes',coalesce(d.rootfs_bytes,0),
    'source_sha256',coalesce(d.source_sha256,''),'parked_reason',coalesce(d.parked_reason,''),
    'scan_status',d.scan_status,'scan_result_hash',encode(sha256(convert_to(coalesce(d.scan_result::text,''),'UTF8')),'hex'),
    'sidecars',coalesce((SELECT jsonb_agg(jsonb_build_object('sidecar_name',r.sidecar_name,
      'storage_key',r.storage_key,'bytes',r.bytes,'content_digest',r.content_digest) ORDER BY r.sidecar_name)
      FROM deployment_sidecar_layers r WHERE r.deployment_id=d.id),'[]'::jsonb)) END)
  || CASE WHEN e.exception_expires_at IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('exception_expires_at_unix_nano',(extract(epoch FROM e.exception_expires_at)*1000000000)::bigint) END
  || CASE WHEN producers IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('runtime_artifacts',producers) END);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd
-- protect_financial_history
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.protect_financial_history() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'DELETE' AND NOT EXISTS(SELECT 1 FROM resource_namespaces WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
  IF TG_TABLE_NAME = 'financial_price_snapshots' AND TG_OP = 'UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'financial history is append-only';
END $$;
-- +goose StatementEnd
-- protect_financial_budget_revision
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.protect_financial_budget_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  -- Account removal may erase its financial data; ordinary policy deletion
  -- is a revisioned tombstone and never removes the audit trail.
  IF TG_OP = 'DELETE' AND NOT EXISTS(SELECT 1 FROM resource_namespaces WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'financial budget revisions are append-only';
END $$;
-- +goose StatementEnd
-- notify_org_app_resource_policy
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_org_app_resource_policy() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE application record;
BEGIN
 IF ROW(NEW.plan,NEW.status,NEW.deleted_pending,NEW.abuse_hold_at,NEW.resource_ownership_enabled) IS NOT DISTINCT FROM
    ROW(OLD.plan,OLD.status,OLD.deleted_pending,OLD.abuse_hold_at,OLD.resource_ownership_enabled) THEN RETURN NEW;END IF;
 FOR application IN SELECT id,account_id FROM apps WHERE org_id=NEW.id ORDER BY id LOOP
  PERFORM pg_notify('app_changed',jsonb_build_object('kind','updated','app_id',application.id,'account_id',application.account_id)::text);
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- bind_app_usage_owner
-- +goose StatementBegin
-- New minute identities use the tenant. ON CONFLICT keeps the existing subject,
-- so a retry straddling deployment never changes a financial record's owner.
CREATE OR REPLACE FUNCTION bind_app_usage_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_org uuid; personal boolean;
BEGIN
    IF NEW.app_id IS NOT NULL AND NEW.meter_kind = 'app' THEN
        SELECT o.id, o.personal_org INTO app_org, personal FROM apps a JOIN orgs o ON o.id=a.org_id
        WHERE a.id=NEW.app_id;
        IF app_org IS NOT NULL THEN
            NEW.org_id := app_org;
            IF NOT personal AND EXISTS(SELECT 1 FROM orgs WHERE id=app_org AND resource_ownership_enabled) THEN NEW.account_id := app_org; END IF;
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 4. triggers, one table per transaction; legacy writers derive the new fields
-- +goose StatementBegin
-- orgs: trigger org_resource_ownership
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_resource_ownership BEFORE INSERT OR UPDATE ON orgs
   FOR EACH ROW EXECUTE FUNCTION activate_org_resource_ownership();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_resource_ownership' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_identities: trigger org_billing_identity
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_billing_identity AFTER INSERT OR UPDATE OF customer_id,subscription_id ON billing_identities
   FOR EACH ROW EXECUTE FUNCTION activate_org_billing_identity();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_identities: trigger org_billing_identity' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- accounts: trigger accounts_shared_app_deletion_transition
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER accounts_shared_app_deletion_transition
       BEFORE UPDATE OF status ON accounts
       FOR EACH ROW WHEN (NEW.status='deleted_pending' AND OLD.status IS DISTINCT FROM NEW.status)
       EXECUTE FUNCTION guard_account_shared_app_deletion_transition();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: accounts: trigger accounts_shared_app_deletion_transition' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: trigger org_streaming_plan
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_streaming_plan BEFORE UPDATE OF plan ON orgs FOR EACH ROW EXECUTE FUNCTION org_streaming_plan_guard();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_streaming_plan' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- org_memberships: trigger org_owner_identity
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_owner_identity BEFORE INSERT OR UPDATE OF role,removed_at,account_id ON org_memberships
   FOR EACH ROW EXECUTE FUNCTION org_owner_identity_active();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: org_memberships: trigger org_owner_identity' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_envs: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON app_envs
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_envs: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secrets: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON app_secrets
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secrets: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- instances: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON instances
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: instances: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON invocations
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- custom_domains: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON custom_domains
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: custom_domains: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- crons: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON crons
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: crons: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- builder_usage: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON builder_usage
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: builder_usage: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- usage_minutes: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON usage_minutes
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: usage_minutes: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- usage_daily: trigger app_core_resource_org
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_core_resource_org BEFORE INSERT OR UPDATE OF app_id,org_id ON usage_daily
   FOR EACH ROW EXECUTE FUNCTION bind_core_app_resource_org();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: usage_daily: trigger app_core_resource_org' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: trigger org_app_resource_policy
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_app_resource_policy AFTER UPDATE OF plan,status,deleted_pending,abuse_hold_at,resource_ownership_enabled ON orgs
   FOR EACH ROW EXECUTE FUNCTION notify_org_app_resource_policy();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_app_resource_policy' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 5. backfills in small batches
-- +goose StatementBegin
-- orgs: adopt company organizations on the Free plan (permanent)
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM orgs WHERE NOT personal_org AND plan = 'free' AND NOT resource_ownership_enabled AND id > last ORDER BY id LIMIT 100) batch;
   IF ids IS NOT NULL THEN
    UPDATE orgs SET resource_ownership_enabled = true WHERE id = ANY (ids) AND NOT personal_org AND plan = 'free' AND NOT resource_ownership_enabled;
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
-- +goose StatementBegin
-- app_envs: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM app_envs c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'app_envs: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE app_envs c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secrets: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM app_secrets c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'app_secrets: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE app_secrets c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- instances: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM instances c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'instances: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE instances c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM invocations c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'invocations: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE invocations c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- custom_domains: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM custom_domains c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'custom_domains: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE custom_domains c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- crons: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM crons c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'crons: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE crons c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- builder_usage: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM builder_usage c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'builder_usage: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE builder_usage c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- usage_minutes: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM usage_minutes c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'usage_minutes: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE usage_minutes c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- usage_daily: company app rows carry the app's organization
DO $online$
DECLARE ctids tid[]; last bigint; found bigint; moved bigint; failures integer := 0; sweeps integer := 0;
BEGIN
 LOOP
  DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
  CREATE TEMP TABLE org_backfill_rows AS
   SELECT row_number() OVER () AS n, c.ctid AS row_ctid FROM usage_daily c
   JOIN apps a ON a.id = c.app_id JOIN orgs o ON o.id = a.org_id
   WHERE NOT o.personal_org AND c.org_id IS DISTINCT FROM a.org_id;
  GET DIAGNOSTICS found = ROW_COUNT;
  CREATE INDEX ON org_backfill_rows (n);
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN found = 0;
  sweeps := sweeps + 1;
  IF sweeps > 100 THEN
   RAISE EXCEPTION 'usage_daily: organization backfill did not converge after 100 sweeps' USING ERRCODE = '55000';
  END IF;
  last := 0;
  WHILE last < found LOOP
   BEGIN
    PERFORM set_config('lock_timeout', '500ms', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    SELECT array_agg(row_ctid) INTO ctids FROM org_backfill_rows WHERE n > last AND n <= last + 500;
    UPDATE usage_daily c SET org_id = a.org_id FROM apps a
     WHERE c.ctid = ANY (ctids) AND a.id = c.app_id AND c.org_id IS DISTINCT FROM a.org_id;
    GET DIAGNOSTICS moved = ROW_COUNT;
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
 DROP TABLE IF EXISTS pg_temp.org_backfill_rows;
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 6. new constraints NOT VALID (writes are checked from here on)
-- +goose StatementBegin
-- orgs: add orgs_suspended_reason_check NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'orgs_suspended_reason_check') THEN
    ALTER TABLE orgs ADD CONSTRAINT orgs_suspended_reason_check CHECK (suspended_reason IS NULL OR suspended_reason IN ('free_quota')) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: add orgs_suspended_reason_check NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- apps: streaming plan check by resource owner, NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_streaming_enabled_plan_check'
                  AND pg_get_constraintdef(oid) LIKE '%apps_streaming_owner_allowed%')
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_streaming_enabled_owner_check') THEN
    ALTER TABLE apps ADD CONSTRAINT apps_streaming_enabled_owner_check
     CHECK (NOT streaming_enabled OR apps_streaming_owner_allowed(account_id,org_id)) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: streaming plan check by resource owner, NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_credits: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_credits'::regclass AND conname = 'account_credits_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_credits'::regclass AND conname = 'org_ns_fk_4063a52147d1') THEN
    ALTER TABLE account_credits ADD CONSTRAINT org_ns_fk_4063a52147d1 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_credits: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_spend_snapshot: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_spend_snapshot'::regclass AND conname = 'account_spend_snapshot_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_spend_snapshot'::regclass AND conname = 'org_ns_fk_db10cbcd128c') THEN
    ALTER TABLE account_spend_snapshot ADD CONSTRAINT org_ns_fk_db10cbcd128c FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_spend_snapshot: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_envs: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_envs'::regclass AND conname = 'app_envs_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_envs'::regclass AND conname = 'org_ns_fk_902d28a7be53') THEN
    ALTER TABLE app_envs ADD CONSTRAINT org_ns_fk_902d28a7be53 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_envs: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_error_requests: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_error_requests'::regclass AND conname = 'app_error_requests_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_error_requests'::regclass AND conname = 'org_ns_fk_d22df47284c1') THEN
    ALTER TABLE app_error_requests ADD CONSTRAINT org_ns_fk_d22df47284c1 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_error_requests: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_errors: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_errors'::regclass AND conname = 'app_errors_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_errors'::regclass AND conname = 'org_ns_fk_1d75c2575178') THEN
    ALTER TABLE app_errors ADD CONSTRAINT org_ns_fk_1d75c2575178 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_errors: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_health_collection_state: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_collection_state'::regclass AND conname = 'app_health_collection_state_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_collection_state'::regclass AND conname = 'org_ns_fk_413a86a2b0e3') THEN
    ALTER TABLE app_health_collection_state ADD CONSTRAINT org_ns_fk_413a86a2b0e3 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_health_collection_state: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_health_history: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_history'::regclass AND conname = 'app_health_history_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_history'::regclass AND conname = 'org_ns_fk_d581b8b2d9ca') THEN
    ALTER TABLE app_health_history ADD CONSTRAINT org_ns_fk_d581b8b2d9ca FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_health_history: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_log_drains: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_log_drains'::regclass AND conname = 'app_log_drains_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_log_drains'::regclass AND conname = 'org_ns_fk_0e5975321dad') THEN
    ALTER TABLE app_log_drains ADD CONSTRAINT org_ns_fk_0e5975321dad FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_log_drains: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_openapi_docs: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_openapi_docs'::regclass AND conname = 'app_openapi_docs_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_openapi_docs'::regclass AND conname = 'org_ns_fk_c0db688e26a6') THEN
    ALTER TABLE app_openapi_docs ADD CONSTRAINT org_ns_fk_c0db688e26a6 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_openapi_docs: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_registry_credentials: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_registry_credentials'::regclass AND conname = 'app_registry_credentials_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_registry_credentials'::regclass AND conname = 'org_ns_fk_948b8bf4d176') THEN
    ALTER TABLE app_registry_credentials ADD CONSTRAINT org_ns_fk_948b8bf4d176 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_registry_credentials: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secret_revocations: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secret_revocations'::regclass AND conname = 'app_secret_revocations_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secret_revocations'::regclass AND conname = 'org_ns_fk_928732de93d6') THEN
    ALTER TABLE app_secret_revocations ADD CONSTRAINT org_ns_fk_928732de93d6 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secret_revocations: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secrets: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secrets'::regclass AND conname = 'app_secrets_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secrets'::regclass AND conname = 'org_ns_fk_d1a74038dced') THEN
    ALTER TABLE app_secrets ADD CONSTRAINT org_ns_fk_d1a74038dced FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secrets: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_service_wake_ahead: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_wake_ahead'::regclass AND conname = 'app_service_wake_ahead_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_wake_ahead'::regclass AND conname = 'org_ns_fk_64cc450df8bb') THEN
    ALTER TABLE app_service_wake_ahead ADD CONSTRAINT org_ns_fk_64cc450df8bb FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_service_wake_ahead: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_tasks: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_tasks'::regclass AND conname = 'app_tasks_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_tasks'::regclass AND conname = 'org_ns_fk_c3f14f103125') THEN
    ALTER TABLE app_tasks ADD CONSTRAINT org_ns_fk_c3f14f103125 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_tasks: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_trusted_signers: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_trusted_signers'::regclass AND conname = 'app_trusted_signers_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_trusted_signers'::regclass AND conname = 'org_ns_fk_29aa287fb3ef') THEN
    ALTER TABLE app_trusted_signers ADD CONSTRAINT org_ns_fk_29aa287fb3ef FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_trusted_signers: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- apps: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'org_ns_fk_9a6dd283c3de') THEN
    ALTER TABLE apps ADD CONSTRAINT org_ns_fk_9a6dd283c3de FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_identities: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_identities'::regclass AND conname = 'billing_identities_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_identities'::regclass AND conname = 'org_ns_fk_4a86ffcaac3f') THEN
    ALTER TABLE billing_identities ADD CONSTRAINT org_ns_fk_4a86ffcaac3f FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_identities: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_meter_usage_deliveries: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_meter_usage_deliveries'::regclass AND conname = 'billing_meter_usage_deliveries_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_meter_usage_deliveries'::regclass AND conname = 'org_ns_fk_c2c272c8c75f') THEN
    ALTER TABLE billing_meter_usage_deliveries ADD CONSTRAINT org_ns_fk_c2c272c8c75f FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_meter_usage_deliveries: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_usage_deliveries: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_usage_deliveries'::regclass AND conname = 'billing_usage_deliveries_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_usage_deliveries'::regclass AND conname = 'org_ns_fk_6f73a99aa257') THEN
    ALTER TABLE billing_usage_deliveries ADD CONSTRAINT org_ns_fk_6f73a99aa257 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_usage_deliveries: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- credit_ledger: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'credit_ledger'::regclass AND conname = 'credit_ledger_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'credit_ledger'::regclass AND conname = 'org_ns_fk_13aa6729892c') THEN
    ALTER TABLE credit_ledger ADD CONSTRAINT org_ns_fk_13aa6729892c FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: credit_ledger: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- deployment_openapi_docs: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_openapi_docs'::regclass AND conname = 'deployment_openapi_docs_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_openapi_docs'::regclass AND conname = 'org_ns_fk_90ec09f45bd4') THEN
    ALTER TABLE deployment_openapi_docs ADD CONSTRAINT org_ns_fk_90ec09f45bd4 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_openapi_docs: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- deployment_scope_exclusions: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_scope_exclusions'::regclass AND conname = 'deployment_scope_exclusions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_scope_exclusions'::regclass AND conname = 'org_ns_fk_9501557f4eac') THEN
    ALTER TABLE deployment_scope_exclusions ADD CONSTRAINT org_ns_fk_9501557f4eac FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_scope_exclusions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_budget_policies: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_budget_policies'::regclass AND conname = 'financial_budget_policies_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_budget_policies'::regclass AND conname = 'org_ns_fk_8b8b7269709c') THEN
    ALTER TABLE financial_budget_policies ADD CONSTRAINT org_ns_fk_8b8b7269709c FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_budget_policies: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_price_snapshots: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_price_snapshots'::regclass AND conname = 'financial_price_snapshots_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_price_snapshots'::regclass AND conname = 'org_ns_fk_92d6df6eb8e2') THEN
    ALTER TABLE financial_price_snapshots ADD CONSTRAINT org_ns_fk_92d6df6eb8e2 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_price_snapshots: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_usage_evidence: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_usage_evidence'::regclass AND conname = 'financial_usage_evidence_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_usage_evidence'::regclass AND conname = 'org_ns_fk_c9e21c99455a') THEN
    ALTER TABLE financial_usage_evidence ADD CONSTRAINT org_ns_fk_c9e21c99455a FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_usage_evidence: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'invocations_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'org_ns_fk_fb47ac6f8935') THEN
    ALTER TABLE invocations ADD CONSTRAINT org_ns_fk_fb47ac6f8935 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invoices: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invoices'::regclass AND conname = 'invoices_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invoices'::regclass AND conname = 'org_ns_fk_56deca22a707') THEN
    ALTER TABLE invoices ADD CONSTRAINT org_ns_fk_56deca22a707 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invoices: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- paddle_overage_dedupe: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'paddle_overage_dedupe'::regclass AND conname = 'paddle_overage_dedupe_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'paddle_overage_dedupe'::regclass AND conname = 'org_ns_fk_3ec32ce40ad2') THEN
    ALTER TABLE paddle_overage_dedupe ADD CONSTRAINT org_ns_fk_3ec32ce40ad2 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: paddle_overage_dedupe: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- request_audit_events: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_audit_events'::regclass AND conname = 'request_audit_events_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_audit_events'::regclass AND conname = 'org_ns_fk_8d7e511ec445') THEN
    ALTER TABLE request_audit_events ADD CONSTRAINT org_ns_fk_8d7e511ec445 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: request_audit_events: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- request_id_journal: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_id_journal'::regclass AND conname = 'request_id_journal_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_id_journal'::regclass AND conname = 'org_ns_fk_4b4eeb20c635') THEN
    ALTER TABLE request_id_journal ADD CONSTRAINT org_ns_fk_4b4eeb20c635 FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: request_id_journal: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- stripe_push_dedupe: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'stripe_push_dedupe'::regclass AND conname = 'stripe_push_dedupe_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'stripe_push_dedupe'::regclass AND conname = 'org_ns_fk_d2eb330f288d') THEN
    ALTER TABLE stripe_push_dedupe ADD CONSTRAINT org_ns_fk_d2eb330f288d FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: stripe_push_dedupe: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_async_quota: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_async_quota'::regclass AND conname = 'account_async_quota_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_async_quota'::regclass AND conname = 'org_ns_fk_5fb6e941fad2') THEN
    ALTER TABLE account_async_quota ADD CONSTRAINT org_ns_fk_5fb6e941fad2 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_async_quota: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 7. validate (SHARE UPDATE EXCLUSIVE: writes continue)
-- +goose StatementBegin
-- orgs: validate orgs_suspended_reason_check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'orgs_suspended_reason_check' AND NOT convalidated) THEN
    ALTER TABLE orgs VALIDATE CONSTRAINT orgs_suspended_reason_check;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: validate orgs_suspended_reason_check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- apps: validate apps_streaming_enabled_owner_check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_streaming_enabled_owner_check' AND NOT convalidated) THEN
    ALTER TABLE apps VALIDATE CONSTRAINT apps_streaming_enabled_owner_check;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: validate apps_streaming_enabled_owner_check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_credits: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_credits'::regclass AND conname = 'org_ns_fk_4063a52147d1' AND NOT convalidated) THEN
    ALTER TABLE account_credits VALIDATE CONSTRAINT org_ns_fk_4063a52147d1;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_credits: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_spend_snapshot: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_spend_snapshot'::regclass AND conname = 'org_ns_fk_db10cbcd128c' AND NOT convalidated) THEN
    ALTER TABLE account_spend_snapshot VALIDATE CONSTRAINT org_ns_fk_db10cbcd128c;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_spend_snapshot: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_envs: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_envs'::regclass AND conname = 'org_ns_fk_902d28a7be53' AND NOT convalidated) THEN
    ALTER TABLE app_envs VALIDATE CONSTRAINT org_ns_fk_902d28a7be53;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_envs: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_error_requests: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_error_requests'::regclass AND conname = 'org_ns_fk_d22df47284c1' AND NOT convalidated) THEN
    ALTER TABLE app_error_requests VALIDATE CONSTRAINT org_ns_fk_d22df47284c1;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_error_requests: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_errors: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_errors'::regclass AND conname = 'org_ns_fk_1d75c2575178' AND NOT convalidated) THEN
    ALTER TABLE app_errors VALIDATE CONSTRAINT org_ns_fk_1d75c2575178;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_errors: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_health_collection_state: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_collection_state'::regclass AND conname = 'org_ns_fk_413a86a2b0e3' AND NOT convalidated) THEN
    ALTER TABLE app_health_collection_state VALIDATE CONSTRAINT org_ns_fk_413a86a2b0e3;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_health_collection_state: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_health_history: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_history'::regclass AND conname = 'org_ns_fk_d581b8b2d9ca' AND NOT convalidated) THEN
    ALTER TABLE app_health_history VALIDATE CONSTRAINT org_ns_fk_d581b8b2d9ca;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_health_history: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_log_drains: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_log_drains'::regclass AND conname = 'org_ns_fk_0e5975321dad' AND NOT convalidated) THEN
    ALTER TABLE app_log_drains VALIDATE CONSTRAINT org_ns_fk_0e5975321dad;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_log_drains: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_openapi_docs: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_openapi_docs'::regclass AND conname = 'org_ns_fk_c0db688e26a6' AND NOT convalidated) THEN
    ALTER TABLE app_openapi_docs VALIDATE CONSTRAINT org_ns_fk_c0db688e26a6;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_openapi_docs: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_registry_credentials: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_registry_credentials'::regclass AND conname = 'org_ns_fk_948b8bf4d176' AND NOT convalidated) THEN
    ALTER TABLE app_registry_credentials VALIDATE CONSTRAINT org_ns_fk_948b8bf4d176;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_registry_credentials: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secret_revocations: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secret_revocations'::regclass AND conname = 'org_ns_fk_928732de93d6' AND NOT convalidated) THEN
    ALTER TABLE app_secret_revocations VALIDATE CONSTRAINT org_ns_fk_928732de93d6;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secret_revocations: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secrets: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secrets'::regclass AND conname = 'org_ns_fk_d1a74038dced' AND NOT convalidated) THEN
    ALTER TABLE app_secrets VALIDATE CONSTRAINT org_ns_fk_d1a74038dced;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secrets: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_service_wake_ahead: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_wake_ahead'::regclass AND conname = 'org_ns_fk_64cc450df8bb' AND NOT convalidated) THEN
    ALTER TABLE app_service_wake_ahead VALIDATE CONSTRAINT org_ns_fk_64cc450df8bb;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_service_wake_ahead: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_tasks: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_tasks'::regclass AND conname = 'org_ns_fk_c3f14f103125' AND NOT convalidated) THEN
    ALTER TABLE app_tasks VALIDATE CONSTRAINT org_ns_fk_c3f14f103125;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_tasks: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_trusted_signers: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_trusted_signers'::regclass AND conname = 'org_ns_fk_29aa287fb3ef' AND NOT convalidated) THEN
    ALTER TABLE app_trusted_signers VALIDATE CONSTRAINT org_ns_fk_29aa287fb3ef;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_trusted_signers: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- apps: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'org_ns_fk_9a6dd283c3de' AND NOT convalidated) THEN
    ALTER TABLE apps VALIDATE CONSTRAINT org_ns_fk_9a6dd283c3de;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_identities: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_identities'::regclass AND conname = 'org_ns_fk_4a86ffcaac3f' AND NOT convalidated) THEN
    ALTER TABLE billing_identities VALIDATE CONSTRAINT org_ns_fk_4a86ffcaac3f;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_identities: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_meter_usage_deliveries: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_meter_usage_deliveries'::regclass AND conname = 'org_ns_fk_c2c272c8c75f' AND NOT convalidated) THEN
    ALTER TABLE billing_meter_usage_deliveries VALIDATE CONSTRAINT org_ns_fk_c2c272c8c75f;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_meter_usage_deliveries: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_usage_deliveries: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_usage_deliveries'::regclass AND conname = 'org_ns_fk_6f73a99aa257' AND NOT convalidated) THEN
    ALTER TABLE billing_usage_deliveries VALIDATE CONSTRAINT org_ns_fk_6f73a99aa257;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_usage_deliveries: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- credit_ledger: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'credit_ledger'::regclass AND conname = 'org_ns_fk_13aa6729892c' AND NOT convalidated) THEN
    ALTER TABLE credit_ledger VALIDATE CONSTRAINT org_ns_fk_13aa6729892c;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: credit_ledger: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- deployment_openapi_docs: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_openapi_docs'::regclass AND conname = 'org_ns_fk_90ec09f45bd4' AND NOT convalidated) THEN
    ALTER TABLE deployment_openapi_docs VALIDATE CONSTRAINT org_ns_fk_90ec09f45bd4;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_openapi_docs: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- deployment_scope_exclusions: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_scope_exclusions'::regclass AND conname = 'org_ns_fk_9501557f4eac' AND NOT convalidated) THEN
    ALTER TABLE deployment_scope_exclusions VALIDATE CONSTRAINT org_ns_fk_9501557f4eac;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_scope_exclusions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_budget_policies: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_budget_policies'::regclass AND conname = 'org_ns_fk_8b8b7269709c' AND NOT convalidated) THEN
    ALTER TABLE financial_budget_policies VALIDATE CONSTRAINT org_ns_fk_8b8b7269709c;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_budget_policies: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_price_snapshots: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_price_snapshots'::regclass AND conname = 'org_ns_fk_92d6df6eb8e2' AND NOT convalidated) THEN
    ALTER TABLE financial_price_snapshots VALIDATE CONSTRAINT org_ns_fk_92d6df6eb8e2;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_price_snapshots: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_usage_evidence: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_usage_evidence'::regclass AND conname = 'org_ns_fk_c9e21c99455a' AND NOT convalidated) THEN
    ALTER TABLE financial_usage_evidence VALIDATE CONSTRAINT org_ns_fk_c9e21c99455a;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_usage_evidence: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'org_ns_fk_fb47ac6f8935' AND NOT convalidated) THEN
    ALTER TABLE invocations VALIDATE CONSTRAINT org_ns_fk_fb47ac6f8935;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invoices: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invoices'::regclass AND conname = 'org_ns_fk_56deca22a707' AND NOT convalidated) THEN
    ALTER TABLE invoices VALIDATE CONSTRAINT org_ns_fk_56deca22a707;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invoices: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- paddle_overage_dedupe: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'paddle_overage_dedupe'::regclass AND conname = 'org_ns_fk_3ec32ce40ad2' AND NOT convalidated) THEN
    ALTER TABLE paddle_overage_dedupe VALIDATE CONSTRAINT org_ns_fk_3ec32ce40ad2;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: paddle_overage_dedupe: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- request_audit_events: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_audit_events'::regclass AND conname = 'org_ns_fk_8d7e511ec445' AND NOT convalidated) THEN
    ALTER TABLE request_audit_events VALIDATE CONSTRAINT org_ns_fk_8d7e511ec445;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: request_audit_events: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- request_id_journal: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_id_journal'::regclass AND conname = 'org_ns_fk_4b4eeb20c635' AND NOT convalidated) THEN
    ALTER TABLE request_id_journal VALIDATE CONSTRAINT org_ns_fk_4b4eeb20c635;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: request_id_journal: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- stripe_push_dedupe: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'stripe_push_dedupe'::regclass AND conname = 'org_ns_fk_d2eb330f288d' AND NOT convalidated) THEN
    ALTER TABLE stripe_push_dedupe VALIDATE CONSTRAINT org_ns_fk_d2eb330f288d;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: stripe_push_dedupe: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_async_quota: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_async_quota'::regclass AND conname = 'org_ns_fk_5fb6e941fad2' AND NOT convalidated) THEN
    ALTER TABLE account_async_quota VALIDATE CONSTRAINT org_ns_fk_5fb6e941fad2;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_async_quota: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 8. retire replaced constraints, one table (and accounts) per transaction
-- +goose StatementBegin
-- apps: replace the streaming plan check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_streaming_enabled_owner_check') THEN
    ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_streaming_enabled_plan_check;
    ALTER TABLE apps RENAME CONSTRAINT apps_streaming_enabled_owner_check TO apps_streaming_enabled_plan_check;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: replace the streaming plan check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_credits: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_credits'::regclass AND conname = 'org_ns_fk_4063a52147d1') THEN
    ALTER TABLE account_credits DROP CONSTRAINT IF EXISTS account_credits_account_id_fkey;
    ALTER TABLE account_credits RENAME CONSTRAINT org_ns_fk_4063a52147d1 TO account_credits_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_credits: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_spend_snapshot: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_spend_snapshot'::regclass AND conname = 'org_ns_fk_db10cbcd128c') THEN
    ALTER TABLE account_spend_snapshot DROP CONSTRAINT IF EXISTS account_spend_snapshot_account_id_fkey;
    ALTER TABLE account_spend_snapshot RENAME CONSTRAINT org_ns_fk_db10cbcd128c TO account_spend_snapshot_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_spend_snapshot: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_envs: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_envs'::regclass AND conname = 'org_ns_fk_902d28a7be53') THEN
    ALTER TABLE app_envs DROP CONSTRAINT IF EXISTS app_envs_account_id_fkey;
    ALTER TABLE app_envs RENAME CONSTRAINT org_ns_fk_902d28a7be53 TO app_envs_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_envs: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_error_requests: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_error_requests'::regclass AND conname = 'org_ns_fk_d22df47284c1') THEN
    ALTER TABLE app_error_requests DROP CONSTRAINT IF EXISTS app_error_requests_account_id_fkey;
    ALTER TABLE app_error_requests RENAME CONSTRAINT org_ns_fk_d22df47284c1 TO app_error_requests_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_error_requests: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_errors: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_errors'::regclass AND conname = 'org_ns_fk_1d75c2575178') THEN
    ALTER TABLE app_errors DROP CONSTRAINT IF EXISTS app_errors_account_id_fkey;
    ALTER TABLE app_errors RENAME CONSTRAINT org_ns_fk_1d75c2575178 TO app_errors_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_errors: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_health_collection_state: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_collection_state'::regclass AND conname = 'org_ns_fk_413a86a2b0e3') THEN
    ALTER TABLE app_health_collection_state DROP CONSTRAINT IF EXISTS app_health_collection_state_account_id_fkey;
    ALTER TABLE app_health_collection_state RENAME CONSTRAINT org_ns_fk_413a86a2b0e3 TO app_health_collection_state_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_health_collection_state: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_health_history: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_health_history'::regclass AND conname = 'org_ns_fk_d581b8b2d9ca') THEN
    ALTER TABLE app_health_history DROP CONSTRAINT IF EXISTS app_health_history_account_id_fkey;
    ALTER TABLE app_health_history RENAME CONSTRAINT org_ns_fk_d581b8b2d9ca TO app_health_history_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_health_history: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_log_drains: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_log_drains'::regclass AND conname = 'org_ns_fk_0e5975321dad') THEN
    ALTER TABLE app_log_drains DROP CONSTRAINT IF EXISTS app_log_drains_account_id_fkey;
    ALTER TABLE app_log_drains RENAME CONSTRAINT org_ns_fk_0e5975321dad TO app_log_drains_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_log_drains: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_openapi_docs: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_openapi_docs'::regclass AND conname = 'org_ns_fk_c0db688e26a6') THEN
    ALTER TABLE app_openapi_docs DROP CONSTRAINT IF EXISTS app_openapi_docs_account_id_fkey;
    ALTER TABLE app_openapi_docs RENAME CONSTRAINT org_ns_fk_c0db688e26a6 TO app_openapi_docs_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_openapi_docs: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_registry_credentials: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_registry_credentials'::regclass AND conname = 'org_ns_fk_948b8bf4d176') THEN
    ALTER TABLE app_registry_credentials DROP CONSTRAINT IF EXISTS app_registry_credentials_account_id_fkey;
    ALTER TABLE app_registry_credentials RENAME CONSTRAINT org_ns_fk_948b8bf4d176 TO app_registry_credentials_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_registry_credentials: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secret_revocations: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secret_revocations'::regclass AND conname = 'org_ns_fk_928732de93d6') THEN
    ALTER TABLE app_secret_revocations DROP CONSTRAINT IF EXISTS app_secret_revocations_account_id_fkey;
    ALTER TABLE app_secret_revocations RENAME CONSTRAINT org_ns_fk_928732de93d6 TO app_secret_revocations_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secret_revocations: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_secrets: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secrets'::regclass AND conname = 'org_ns_fk_d1a74038dced') THEN
    ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_account_id_fkey;
    ALTER TABLE app_secrets RENAME CONSTRAINT org_ns_fk_d1a74038dced TO app_secrets_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_secrets: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_service_wake_ahead: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_service_wake_ahead'::regclass AND conname = 'org_ns_fk_64cc450df8bb') THEN
    ALTER TABLE app_service_wake_ahead DROP CONSTRAINT IF EXISTS app_service_wake_ahead_account_id_fkey;
    ALTER TABLE app_service_wake_ahead RENAME CONSTRAINT org_ns_fk_64cc450df8bb TO app_service_wake_ahead_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_service_wake_ahead: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_tasks: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_tasks'::regclass AND conname = 'org_ns_fk_c3f14f103125') THEN
    ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_account_id_fkey;
    ALTER TABLE app_tasks RENAME CONSTRAINT org_ns_fk_c3f14f103125 TO app_tasks_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_tasks: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- app_trusted_signers: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_trusted_signers'::regclass AND conname = 'org_ns_fk_29aa287fb3ef') THEN
    ALTER TABLE app_trusted_signers DROP CONSTRAINT IF EXISTS app_trusted_signers_account_id_fkey;
    ALTER TABLE app_trusted_signers RENAME CONSTRAINT org_ns_fk_29aa287fb3ef TO app_trusted_signers_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: app_trusted_signers: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- apps: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'org_ns_fk_9a6dd283c3de') THEN
    ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_account_id_fkey;
    ALTER TABLE apps RENAME CONSTRAINT org_ns_fk_9a6dd283c3de TO apps_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_identities: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_identities'::regclass AND conname = 'org_ns_fk_4a86ffcaac3f') THEN
    ALTER TABLE billing_identities DROP CONSTRAINT IF EXISTS billing_identities_account_id_fkey;
    ALTER TABLE billing_identities RENAME CONSTRAINT org_ns_fk_4a86ffcaac3f TO billing_identities_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_identities: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_meter_usage_deliveries: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_meter_usage_deliveries'::regclass AND conname = 'org_ns_fk_c2c272c8c75f') THEN
    ALTER TABLE billing_meter_usage_deliveries DROP CONSTRAINT IF EXISTS billing_meter_usage_deliveries_account_id_fkey;
    ALTER TABLE billing_meter_usage_deliveries RENAME CONSTRAINT org_ns_fk_c2c272c8c75f TO billing_meter_usage_deliveries_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_meter_usage_deliveries: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- billing_usage_deliveries: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'billing_usage_deliveries'::regclass AND conname = 'org_ns_fk_6f73a99aa257') THEN
    ALTER TABLE billing_usage_deliveries DROP CONSTRAINT IF EXISTS billing_usage_deliveries_account_id_fkey;
    ALTER TABLE billing_usage_deliveries RENAME CONSTRAINT org_ns_fk_6f73a99aa257 TO billing_usage_deliveries_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: billing_usage_deliveries: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- credit_ledger: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'credit_ledger'::regclass AND conname = 'org_ns_fk_13aa6729892c') THEN
    ALTER TABLE credit_ledger DROP CONSTRAINT IF EXISTS credit_ledger_account_id_fkey;
    ALTER TABLE credit_ledger RENAME CONSTRAINT org_ns_fk_13aa6729892c TO credit_ledger_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: credit_ledger: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- deployment_openapi_docs: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_openapi_docs'::regclass AND conname = 'org_ns_fk_90ec09f45bd4') THEN
    ALTER TABLE deployment_openapi_docs DROP CONSTRAINT IF EXISTS deployment_openapi_docs_account_id_fkey;
    ALTER TABLE deployment_openapi_docs RENAME CONSTRAINT org_ns_fk_90ec09f45bd4 TO deployment_openapi_docs_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_openapi_docs: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- deployment_scope_exclusions: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_scope_exclusions'::regclass AND conname = 'org_ns_fk_9501557f4eac') THEN
    ALTER TABLE deployment_scope_exclusions DROP CONSTRAINT IF EXISTS deployment_scope_exclusions_account_id_fkey;
    ALTER TABLE deployment_scope_exclusions RENAME CONSTRAINT org_ns_fk_9501557f4eac TO deployment_scope_exclusions_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_scope_exclusions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_budget_policies: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_budget_policies'::regclass AND conname = 'org_ns_fk_8b8b7269709c') THEN
    ALTER TABLE financial_budget_policies DROP CONSTRAINT IF EXISTS financial_budget_policies_account_id_fkey;
    ALTER TABLE financial_budget_policies RENAME CONSTRAINT org_ns_fk_8b8b7269709c TO financial_budget_policies_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_budget_policies: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_price_snapshots: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_price_snapshots'::regclass AND conname = 'org_ns_fk_92d6df6eb8e2') THEN
    ALTER TABLE financial_price_snapshots DROP CONSTRAINT IF EXISTS financial_price_snapshots_account_id_fkey;
    ALTER TABLE financial_price_snapshots RENAME CONSTRAINT org_ns_fk_92d6df6eb8e2 TO financial_price_snapshots_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_price_snapshots: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- financial_usage_evidence: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'financial_usage_evidence'::regclass AND conname = 'org_ns_fk_c9e21c99455a') THEN
    ALTER TABLE financial_usage_evidence DROP CONSTRAINT IF EXISTS financial_usage_evidence_account_id_fkey;
    ALTER TABLE financial_usage_evidence RENAME CONSTRAINT org_ns_fk_c9e21c99455a TO financial_usage_evidence_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: financial_usage_evidence: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'org_ns_fk_fb47ac6f8935') THEN
    ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocations_account_id_fkey;
    ALTER TABLE invocations RENAME CONSTRAINT org_ns_fk_fb47ac6f8935 TO invocations_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invoices: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invoices'::regclass AND conname = 'org_ns_fk_56deca22a707') THEN
    ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_account_id_fkey;
    ALTER TABLE invoices RENAME CONSTRAINT org_ns_fk_56deca22a707 TO invoices_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invoices: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- paddle_overage_dedupe: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'paddle_overage_dedupe'::regclass AND conname = 'org_ns_fk_3ec32ce40ad2') THEN
    ALTER TABLE paddle_overage_dedupe DROP CONSTRAINT IF EXISTS paddle_overage_dedupe_account_id_fkey;
    ALTER TABLE paddle_overage_dedupe RENAME CONSTRAINT org_ns_fk_3ec32ce40ad2 TO paddle_overage_dedupe_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: paddle_overage_dedupe: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- request_audit_events: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_audit_events'::regclass AND conname = 'org_ns_fk_8d7e511ec445') THEN
    ALTER TABLE request_audit_events DROP CONSTRAINT IF EXISTS request_audit_events_account_id_fkey;
    ALTER TABLE request_audit_events RENAME CONSTRAINT org_ns_fk_8d7e511ec445 TO request_audit_events_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: request_audit_events: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- request_id_journal: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_id_journal'::regclass AND conname = 'org_ns_fk_4b4eeb20c635') THEN
    ALTER TABLE request_id_journal DROP CONSTRAINT IF EXISTS request_id_journal_account_id_fkey;
    ALTER TABLE request_id_journal RENAME CONSTRAINT org_ns_fk_4b4eeb20c635 TO request_id_journal_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: request_id_journal: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- stripe_push_dedupe: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'stripe_push_dedupe'::regclass AND conname = 'org_ns_fk_d2eb330f288d') THEN
    ALTER TABLE stripe_push_dedupe DROP CONSTRAINT IF EXISTS stripe_push_dedupe_account_id_fkey;
    ALTER TABLE stripe_push_dedupe RENAME CONSTRAINT org_ns_fk_d2eb330f288d TO stripe_push_dedupe_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: stripe_push_dedupe: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- account_async_quota: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_async_quota'::regclass AND conname = 'org_ns_fk_5fb6e941fad2') THEN
    ALTER TABLE account_async_quota DROP CONSTRAINT IF EXISTS account_async_quota_account_id_fkey;
    ALTER TABLE account_async_quota RENAME CONSTRAINT org_ns_fk_5fb6e941fad2 TO account_async_quota_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: account_async_quota: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 9. attribute adopted company usage to the organization
-- +goose StatementBegin
-- builder_usage: trigger app_builder_owner
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_builder_owner BEFORE INSERT ON builder_usage FOR EACH ROW EXECUTE FUNCTION bind_app_builder_owner();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: builder_usage: trigger app_builder_owner' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- snapshot_storage_daily: trigger app_storage_owner
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_storage_owner BEFORE INSERT ON snapshot_storage_daily FOR EACH ROW EXECUTE FUNCTION bind_app_storage_owner();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: snapshot_storage_daily: trigger app_storage_owner' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- usage_minutes: trigger app_usage_owner
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER app_usage_owner BEFORE INSERT ON usage_minutes
   FOR EACH ROW EXECUTE FUNCTION bind_app_usage_owner();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: usage_minutes: trigger app_usage_owner' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
-- Rollback is an operator action on a drained schema, not part of a release:
-- releases roll back by running the previous binaries against this schema.
SET lock_timeout = '5s';
-- Contraction cannot reconnect resources to erased identities. Refuse instead
-- of silently losing company resources or fabricating replacement humans.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM apps a LEFT JOIN accounts ac ON ac.id=a.account_id WHERE ac.id IS NULL)
       OR EXISTS (SELECT 1 FROM billing_identities b LEFT JOIN accounts ac ON ac.id=b.account_id WHERE ac.id IS NULL) THEN
        RAISE EXCEPTION 'organization billing adoption or erased identities prevent lifecycle downgrade';
    END IF;
END $$;
DROP TRIGGER org_owner_identity ON org_memberships;
DROP FUNCTION org_owner_identity_active();
DROP TRIGGER org_streaming_plan ON orgs;
DROP FUNCTION org_streaming_plan_guard();
ALTER TABLE apps DROP CONSTRAINT apps_streaming_enabled_plan_check;
ALTER TABLE apps ADD CONSTRAINT apps_streaming_enabled_plan_check CHECK (NOT streaming_enabled OR apps_streaming_plan_allowed(account_id));
DROP FUNCTION apps_streaming_owner_allowed(uuid,uuid);
CREATE OR REPLACE FUNCTION public.apps_streaming_plan_account_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.plan = 'free'
       AND OLD.plan IS DISTINCT FROM NEW.plan
       AND EXISTS (
           SELECT 1
             FROM apps
            WHERE account_id = NEW.id
              AND streaming_enabled
       ) THEN
        RAISE EXCEPTION
            'account % cannot downgrade to Free while a streaming-enabled app exists',
            NEW.id
            USING ERRCODE = '23514',
                  CONSTRAINT = 'apps_streaming_enabled_plan_check';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION public.apps_streaming_plan_app_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.streaming_enabled
       AND (TG_OP = 'INSERT'
            OR NOT OLD.streaming_enabled
            OR OLD.account_id IS DISTINCT FROM NEW.account_id) THEN
        PERFORM 1
          FROM accounts
         WHERE id = NEW.account_id
         FOR NO KEY UPDATE;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER app_storage_owner ON snapshot_storage_daily;
DROP FUNCTION bind_app_storage_owner();
DROP TRIGGER app_builder_owner ON builder_usage;
DROP FUNCTION bind_app_builder_owner();
CREATE OR REPLACE FUNCTION public.retain_financial_usage_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  previous_compute bigint := 0;
  previous_egress bigint := 0;
  captured_plan text;
  captured_attribution jsonb;
  delta bigint;
  meter_name text;
  unit_name text;
  cumulative bigint;
  captured_price text;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    previous_compute := OLD.mb_seconds;
    previous_egress := OLD.net_tx_bytes;
  END IF;
  IF NEW.mb_seconds = previous_compute AND NEW.net_tx_bytes = previous_egress THEN RETURN NEW; END IF;
  IF NEW.mb_seconds < previous_compute OR NEW.net_tx_bytes < previous_egress THEN
    RAISE EXCEPTION 'financial usage corrections require retained adjustment lineage';
  END IF;
  -- Allocate evidence IDs in account commit order. Without this transaction
  -- lock, a lower sequence could commit after a paged reader fixed its head.
  PERFORM pg_advisory_xact_lock(hashtextextended('financial-evidence:' || NEW.account_id::text, 0));

  -- Preserve the first observation's identity even if the live workload is
  -- renamed, moved or deleted before later network observations arrive.
  SELECT plan, attribution INTO captured_plan, captured_attribution
    FROM financial_usage_evidence WHERE account_id = NEW.account_id
      AND instance_id = NEW.instance_id AND source_start = NEW.minute
    ORDER BY id LIMIT 1;
  IF NOT FOUND THEN
    -- Recorded activations apply to source intervals. In particular, the
    -- just-closed minute still uses its contract when the live plan changes.
    SELECT COALESCE((
      SELECT price.plan FROM financial_price_snapshots price
      WHERE price.account_id = NEW.account_id AND price.meter = 'compute'
        AND price.period_start <= NEW.minute AND price.period_end > NEW.minute
        AND price.effective_from <= NEW.minute
      ORDER BY price.effective_from DESC, price.recorded_at DESC, price.version
      LIMIT 1
    ), account.plan), jsonb_strip_nulls(jsonb_build_object(
      'app_id', NEW.app_id::text, 'job_id', NEW.job_id::text,
      'project_id', app.project_id::text, 'deployment_id', instance.deployment_id::text,
      'environment_id', environment.id::text, 'name', COALESCE(app.slug, job.name)))
    INTO captured_plan, captured_attribution
    FROM accounts account
    LEFT JOIN apps app ON app.id = NEW.app_id AND app.account_id = account.id
    LEFT JOIN jobs job ON job.id = NEW.job_id AND job.account_id = account.id
    LEFT JOIN instances instance ON instance.id = NEW.instance_id AND instance.app_id = app.id
    LEFT JOIN deployments deployment ON deployment.id = instance.deployment_id AND deployment.app_id = app.id
    LEFT JOIN project_environments environment ON environment.account_id = account.id
      AND environment.project_id = app.project_id AND environment.slug = deployment.scope
    WHERE account.id = NEW.account_id;
    -- Preserve canonical usage behavior for orphan fixtures and late samples
    -- after account deletion without recreating erased financial history.
    IF NOT FOUND THEN RETURN NEW; END IF;
  END IF;
  FOR meter_name, unit_name, delta, cumulative IN
    SELECT 'compute', 'mb_seconds', NEW.mb_seconds - previous_compute, NEW.mb_seconds
    UNION ALL SELECT 'egress', 'interface_bytes', NEW.net_tx_bytes - previous_egress, NEW.net_tx_bytes
  LOOP
    IF delta > 0 THEN
      SELECT version INTO captured_price FROM financial_price_snapshots
        WHERE account_id = NEW.account_id AND meter = meter_name AND plan = captured_plan
          AND period_start <= NEW.minute AND period_end > NEW.minute AND effective_from <= NEW.minute
        ORDER BY effective_from DESC, recorded_at DESC, version LIMIT 1;
      INSERT INTO financial_usage_evidence(account_id, instance_id, source_id, meter, unit, quantity,
        source_start, source_end, plan, attribution, price_version)
      VALUES (NEW.account_id, NEW.instance_id,
        'usage:' || NEW.instance_id::text || ':' || extract(epoch FROM NEW.minute)::text || ':' || meter_name || ':' || cumulative::text,
        meter_name, unit_name, delta, NEW.minute, NEW.minute + interval '1 minute', captured_plan, captured_attribution, captured_price);
    END IF;
  END LOOP;
  RETURN NEW;
END $$;
DROP VIEW billing_resource_accounts;

DROP TRIGGER app_core_resource_org ON app_envs;
DROP TRIGGER app_core_resource_org ON app_secrets;
DROP TRIGGER app_core_resource_org ON instances;
DROP TRIGGER app_core_resource_org ON invocations;
DROP TRIGGER app_core_resource_org ON custom_domains;
DROP TRIGGER app_core_resource_org ON crons;
DROP TRIGGER app_core_resource_org ON builder_usage;
DROP TRIGGER app_core_resource_org ON usage_minutes;
DROP TRIGGER app_core_resource_org ON usage_daily;
DROP FUNCTION bind_core_app_resource_org();

DROP TRIGGER app_usage_owner ON usage_minutes;
DROP FUNCTION bind_app_usage_owner();
CREATE OR REPLACE FUNCTION public.application_standard_egress_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'standard egress inputs are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,acct,e NOWAIT;
 PERFORM id FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 PERFORM id FROM instances WHERE app_id=NEW.app_id AND node_id=NEW.node_id
  AND state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining') FOR SHARE NOWAIT;
 actual:=application_standard_egress_target(NEW.app_id,NEW.node_id);
 IF actual IS NULL OR actual<>NEW.target OR actual->>'org_id'<>NEW.org_id::text
  OR actual->'identity'->>'ProtocolVersion'<>'2' OR actual->'identity'->>'Incarnation'=''
  OR NEW.receipt<>jsonb_build_object('identity',actual->'identity','app_id',actual->>'app_id',
   'revision',actual->'policy'->'revision','policy_hash',actual->>'policy_hash') THEN
  RAISE EXCEPTION 'standard egress process or inputs changed' USING ERRCODE='40001',CONSTRAINT='application_standard_egress_current';
 END IF;
 NEW.observed_at:=clock_timestamp();
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.application_standard_egress_target(app uuid, node uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $_$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,
 'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
 'identity',jsonb_build_object('ProtocolVersion',coalesce(n.vmmd_admission_protocol,0),'NodeID',n.id::text,'Incarnation',coalesce(n.vmmd_incarnation::text,'')),
 'policy',jsonb_build_object('app_id',a.id::text,'revision',a.egress_allowlist_revision,'allowlist',to_jsonb(a.egress_allowlist::text[]),'ports',to_jsonb(a.egress_ports)),
 'policy_hash',application_standard_egress_policy_hash(a))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id JOIN compute_nodes n ON n.id=node
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND e.effective_hash~'^[a-f0-9]{64}$' AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND n.active AND n.role IS DISTINCT FROM 'control-plane'
 AND EXISTS(SELECT 1 FROM instances i WHERE i.app_id=a.id AND i.node_id=n.id
  AND i.state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining'));
$_$;

CREATE OR REPLACE FUNCTION public.application_standard_lock_observation_evidence(application_id uuid) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||application_id::text,0)) THEN
  RAISE EXCEPTION 'application standard controls are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 WHERE a.id=application_id FOR SHARE OF o,acct NOWAIT;
 PERFORM p.id FROM projects p JOIN apps a ON a.project_id=p.id WHERE a.id=application_id FOR SHARE OF p NOWAIT;
 PERFORM d.id FROM app_log_drains d WHERE d.app_id=application_id ORDER BY d.id FOR SHARE NOWAIT;
 PERFORM b.app_id FROM application_standard_control_bindings b WHERE b.app_id=application_id
 ORDER BY b.field,b.resource_id FOR SHARE NOWAIT;
 PERFORM r.id FROM application_standard_log_destinations r JOIN application_standard_control_bindings b
 ON b.resource_id=r.id AND b.field='log_destinations' WHERE b.app_id=application_id ORDER BY r.id FOR SHARE OF r NOWAIT;
 PERFORM x.app_id FROM application_standard_log_inventories x WHERE x.app_id=application_id ORDER BY x.node_id FOR SHARE NOWAIT;
 PERFORM h.app_id FROM application_standard_log_health h WHERE h.app_id=application_id ORDER BY h.node_id,h.drain_id FOR SHARE NOWAIT;
 PERFORM e.app_id FROM application_standard_egress_observations e WHERE e.app_id=application_id ORDER BY e.node_id FOR SHARE NOWAIT;
 RETURN true;
END;
$$;

CREATE OR REPLACE FUNCTION public.application_standard_log_binding(app uuid, drain uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object('org_id',e.org_id::text,'app_id',a.id::text,'drain_id',d.id::text,
  'resource_id',r.id::text,'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
  'resource_config_hash',r.config_hash,'drain_config_hash',application_standard_log_drain_hash(d))
FROM app_log_drains d
 JOIN apps a ON a.id=d.app_id JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id
 JOIN application_standard_control_bindings b ON b.app_id=a.id AND b.field='log_destinations' AND b.physical_id=d.id::text
 JOIN application_standard_log_destinations r ON r.id=b.resource_id
 WHERE d.app_id=app AND d.id=drain AND d.enabled AND a.status='active' AND d.account_id=a.account_id
 AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND jsonb_array_length(coalesce(e.effective->'sources'->'log_destinations','[]'::jsonb))>0
 AND (coalesce(e.effective->'values'->'log_destinations','[]'::jsonb) ? b.resource_id::text
      OR b.resource_id=ANY(e.additional_log_destinations))
 AND r.org_id=e.org_id;
$$;

CREATE OR REPLACE FUNCTION public.application_standard_log_health_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'logging health projection is busy' USING ERRCODE='55P03';
 END IF;
 PERFORM h.app_id FROM application_standard_log_health h
  WHERE h.app_id=NEW.app_id AND h.drain_id=NEW.drain_id AND h.node_id=NEW.node_id FOR UPDATE NOWAIT;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
  JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,acct,e NOWAIT;
 PERFORM d.id FROM app_log_drains d JOIN application_standard_control_bindings b ON b.app_id=d.app_id AND b.field='log_destinations' AND b.physical_id=d.id::text
  JOIN application_standard_log_destinations r ON r.id=b.resource_id
  WHERE d.app_id=NEW.app_id AND d.id=NEW.drain_id FOR SHARE OF d,b,r NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id FOR SHARE OF c,n NOWAIT;
 IF NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation
   AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'logging health consumer changed' USING ERRCODE='55000';
 END IF;
 IF NEW.status='healthy' THEN
  PERFORM i.id FROM instances i WHERE i.id=NEW.source_instance_id AND i.app_id=NEW.app_id FOR SHARE NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'logging health source changed' USING ERRCODE='40001'; END IF;
 END IF;
 actual:=application_standard_log_binding(NEW.app_id,NEW.drain_id);
 IF actual IS NULL OR actual<>NEW.binding OR actual->>'org_id'<>NEW.org_id::text THEN
  RAISE EXCEPTION 'logging health binding changed' USING ERRCODE='40001';
 END IF;
 NEW.observed_at:=clock_timestamp();
 NEW.event_at:=NEW.observed_at;
 IF TG_OP='UPDATE' THEN
  IF (NEW.app_id,NEW.drain_id,NEW.node_id) IS DISTINCT FROM (OLD.app_id,OLD.drain_id,OLD.node_id) THEN
   RAISE EXCEPTION 'logging health identity changed' USING ERRCODE='40001';
  END IF;
  IF NEW.binding=OLD.binding AND NEW.session_id=OLD.session_id AND NEW.generation=OLD.generation THEN
   IF NEW.event_revision<OLD.event_revision OR (NEW.event_revision=OLD.event_revision AND
    (NEW.status,NEW.reason,NEW.source_instance_id,NEW.sequence) IS DISTINCT FROM (OLD.status,OLD.reason,OLD.source_instance_id,OLD.sequence)) THEN
    RAISE EXCEPTION 'logging health event superseded' USING ERRCODE='GS001';
   END IF;
   IF NEW.event_revision=OLD.event_revision THEN NEW.event_at:=OLD.event_at; END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.application_standard_log_inventory(app uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,'account_id',a.account_id::text,
  'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,'drains',
  coalesce((SELECT jsonb_agg(jsonb_build_object('drain_id',d.id::text,'config_hash',application_standard_log_drain_hash(d)) ORDER BY d.id)
   FROM app_log_drains d WHERE d.app_id=a.id AND d.enabled),'[]'::jsonb))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp());
$$;

CREATE OR REPLACE FUNCTION public.application_standard_log_inventory_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 -- This nonwaiting exclusive fence also covers drain inserts/deletes, not just existing rows.
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'logging inventory is busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=NEW.app_id FOR SHARE OF a,o,acct,e NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
 WHERE c.node_id=NEW.node_id FOR SHARE OF c,n NOWAIT;
 NEW.observed_at:=clock_timestamp();
 actual:=application_standard_log_inventory(NEW.app_id);
 IF actual IS NULL OR actual<>NEW.inventory OR actual->>'org_id'<>NEW.org_id::text THEN
  RAISE EXCEPTION 'application standard logging inventory changed' USING ERRCODE='40001';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation
   AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'application standard logging consumer changed' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.capture_instance_capacity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE p service_capacity_policy%ROWTYPE; main_cpu integer; guest_vcpu integer; side_ram bigint; side_cpu bigint;
BEGIN
    IF TG_OP='UPDATE' THEN
        NEW.capacity_ram_mb:=greatest(OLD.capacity_ram_mb,NEW.capacity_ram_mb);
        NEW.capacity_cpu_millicores:=greatest(OLD.capacity_cpu_millicores,NEW.capacity_cpu_millicores);
        NEW.capacity_vcpu:=greatest(OLD.capacity_vcpu,NEW.capacity_vcpu);
        RETURN NEW;
    END IF;
    SELECT * INTO STRICT p FROM service_capacity_policy WHERE singleton;
    SELECT greatest(a.cpu_millicores,p.startup_cpu),coalesce((p.plan_vcpus->>ac.plan)::integer,4)
      INTO main_cpu,guest_vcpu FROM apps a JOIN accounts ac ON ac.id=a.account_id WHERE a.id=NEW.app_id;
    SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0),
           coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
      INTO side_ram,side_cpu FROM deployments d,
        LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=NEW.deployment_id;
    NEW.capacity_ram_mb:=greatest(NEW.capacity_ram_mb,NEW.ram_mb+p.overhead_mb+side_ram);
    NEW.capacity_cpu_millicores:=greatest(NEW.capacity_cpu_millicores,coalesce(main_cpu,p.startup_cpu)+side_cpu);
    NEW.capacity_vcpu:=greatest(NEW.capacity_vcpu,coalesce(guest_vcpu,1));
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_instance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; app_ram integer; may_deploy boolean; b environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM environment_qualification_executions WHERE instance_id=NEW.id) THEN
  RAISE EXCEPTION 'retained qualification instance cannot be reused' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments d
   JOIN environment_git_sources s ON s.id=(d.environment_workload_runtime->>'source_id')::uuid
   JOIN project_environments e ON e.id=(d.environment_workload_runtime->>'environment_id')::uuid WHERE d.id=OLD.deployment_id) THEN
   SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=OLD.id OR id=(SELECT request_id FROM environment_qualification_restore_reservations WHERE instance_id=OLD.id) FOR UPDATE;
   IF OLD.state NOT IN ('parked','stopped','failed') OR (q.id IS NOT NULL AND q.lease_until>clock_timestamp()) THEN
    RAISE EXCEPTION 'environment qualification reservation must remain until retired and its attempt released' USING ERRCODE='23514';
   END IF;
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL)
  AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.ram_mb,NEW.kind,NEW.job_id)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.ram_mb,OLD.kind,OLD.job_id) THEN
  RAISE EXCEPTION 'environment qualification instance identity is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=NEW.id;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id OR
  (b.instance_id IS NOT NULL AND id=b.request_id AND reserved_instance_id=b.capture_instance_id AND attempt=b.attempt);
 IF q.id IS NOT NULL AND (q.app_id IS DISTINCT FROM NEW.app_id OR q.deployment_id IS DISTINCT FROM NEW.deployment_id) THEN
  RAISE EXCEPTION 'environment qualification reservation cannot be reassigned' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND q.id IS NULL AND NEW.state IN ('draining','snapshotting','parked','stopped','failed','evicting_account_deleting') AND
  EXISTS(SELECT 1 FROM environment_workload_retirements r JOIN deployments d ON d.id=OLD.deployment_id
   WHERE d.id=ANY(r.deployment_ids) AND d.status='superseded' AND NOT d.environment_workload_held AND r.phase NOT IN ('planned','cancelled')) THEN RETURN NEW; END IF;
 IF q.id IS NULL AND EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND app_id=NEW.app_id
  AND environment_workload_runtime IS NOT NULL AND NOT environment_workload_held AND status='live') THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment qualification instances must be created under their attempt' USING ERRCODE='23514';
 END IF;
 -- Retirement must remain available after expiry, supersession and parent
 -- deletion. It cannot grant execution or qualification evidence.
 IF TG_OP='UPDATE' AND NEW.state IN ('parked','stopped','failed','evicting_account_deleting') THEN RETURN NEW; END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id
   WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=q.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR (q.execution_mode='job' AND
    (coalesce(jsonb_typeof(q.frozen_inputs->'job_smoke'),'null')<>'object' OR
     coalesce(nullif(q.frozen_inputs->'queue_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb
     OR NOT EXISTS(SELECT 1 FROM environment_workload_graphs g, jsonb_array_elements(g.members) member
       WHERE g.id=q.graph_id AND member->>'resource'=q.resource AND member->>'execution_mode'='job'
        AND coalesce((member->>'job_smoke_configured')::boolean,false)
        AND coalesce(nullif(member->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)
        AND coalesce((member->>'service_bindings_configured')::boolean,false)=(coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb)
        AND coalesce(member->>'queue_bindings_configured','false')='false'
        AND coalesce(nullif(member->'queue_bindings','null'::jsonb),'{}'::jsonb)='{}'::jsonb
        AND coalesce(nullif(member->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb))) OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>NEW.app_id OR NEW.kind<>'wake' OR NEW.job_id IS NOT NULL
  OR NEW.mode IS DISTINCT FROM (CASE q.execution_mode WHEN 'worker' THEN 'worker' WHEN 'service' THEN 'service' ELSE 'normal' END)
  OR (b.instance_id IS NOT NULL AND (NOT environment_qualification_restore_current(q.id,b.capture_instance_id)
   OR NEW.wake_id=(SELECT (frame->>'wake_id')::uuid FROM environment_qualification_executions WHERE instance_id=b.capture_instance_id)))
  OR NOT environment_workload_qualification_inputs_current(q.id) THEN
  RAISE EXCEPTION 'environment workload execution requires its current qualification attempt' USING ERRCODE='23514';
 END IF;
 SELECT a.ram_mb,c.status='active' AND c.abuse_hold_at IS NULL INTO app_ram,may_deploy
  FROM apps a JOIN accounts c ON c.id=a.account_id WHERE a.id=NEW.app_id FOR SHARE OF c;
 IF NOT coalesce(may_deploy,false) OR NEW.ram_mb IS DISTINCT FROM app_ram OR NOT EXISTS(
  SELECT 1 FROM compute_nodes WHERE id=NEW.node_id AND active AND lifecycle='active') OR
  (TG_OP='INSERT' AND (NEW.state<>'cold_booting' OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL
   OR coalesce(NEW.guest_uid,0)<>0 OR NEW.framework_ready_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'environment qualification admission shape is invalid' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION public.guard_environment_workload_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
 row_value jsonb; prior jsonb; src environment_git_sources%ROWTYPE;
 resource_name text; paths text[]; controller boolean;
BEGIN
 IF TG_OP='DELETE' THEN row_value:=to_jsonb(OLD); ELSE row_value:=to_jsonb(NEW); END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM apps a JOIN accounts c ON c.id=a.account_id
  JOIN project_environments e ON e.account_id=a.account_id AND e.project_id=a.project_id
  WHERE a.id=OLD.app_id AND a.account_id=OLD.account_id AND e.id=OLD.environment_id) THEN
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.environment_id IS DISTINCT FROM OLD.environment_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_workload_intent_identity',MESSAGE='scoped workload identity is immutable';
 END IF;
 IF TG_OP<>'DELETE' AND NOT EXISTS(SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
  WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id AND e.id=NEW.environment_id AND a.status<>'deleted') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_workload_intent_identity',MESSAGE='scoped workload requires its original project environment';
 END IF;
 SELECT s.* INTO src FROM active_environment_git_sources s WHERE s.account_id=(row_value->>'account_id')::uuid
  AND s.environment_id=(row_value->>'environment_id')::uuid FOR UPDATE OF s;
 IF TG_OP='INSERT' THEN
  SELECT to_jsonb(w) INTO prior FROM app_environment_workload_intents w
   WHERE w.app_id=NEW.app_id AND w.environment_id=NEW.environment_id FOR UPDATE OF w;
  prior:=coalesce(prior,'{}');
 ELSE prior:=to_jsonb(OLD); END IF;
 IF TG_OP='DELETE' THEN row_value:=row_value||jsonb_build_object('source',NULL,'source_revision',NULL,'runtime','{}'::jsonb,'service_bindings','{}'::jsonb,'schedule',NULL,'variables','{}'::jsonb); END IF;
 SELECT array_agg('runtime/'||k) INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'runtime','{}')) UNION SELECT key FROM jsonb_each(row_value->'runtime')
 ) keys WHERE (prior->'runtime'->k) IS DISTINCT FROM (row_value->'runtime'->k);
 IF coalesce(prior->'source','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source']; END IF;
 IF coalesce(prior->'source_revision','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source_revision','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source_revision','source']; END IF;
 SELECT coalesce(paths,'{}')||coalesce(array_agg('service_bindings/'||k),'{}') INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'service_bindings','{}')) UNION SELECT key FROM jsonb_each(row_value->'service_bindings')
 ) keys WHERE (prior->'service_bindings'->k) IS DISTINCT FROM (row_value->'service_bindings'->k);
 IF prior->'schedule' IS DISTINCT FROM row_value->'schedule' THEN paths:=coalesce(paths,'{}')||ARRAY['schedule']; END IF;
 SELECT coalesce(paths,'{}')||coalesce(array_agg('variables/'||k),'{}') INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'variables','{}')) UNION SELECT key FROM jsonb_each(row_value->'variables')
 ) keys WHERE (prior->'variables'->k) IS DISTINCT FROM (row_value->'variables'->k);
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.service_bindings) b WHERE
  b.key !~ '^[a-z0-9][a-z0-9-]*$' OR jsonb_typeof(b.value)<>'object' OR
  NOT b.value ?& ARRAY['workload','env_key','target_app_id'] OR b.value->>'env_key' !~ '^[A-Za-z_][A-Za-z0-9_]*$' OR
  NOT EXISTS(SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id
   WHERE r.source_id=src.id AND r.logical_name='workload/'||(b.value->>'workload') AND a.id=(b.value->>'target_app_id')::uuid
   AND a.id<>NEW.app_id AND a.account_id=NEW.account_id AND a.project_id=src.project_id AND a.status<>'deleted')) THEN
  RAISE EXCEPTION 'scoped service binding requires its original environment target' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.variables) v WHERE
  v.key !~ '^[A-Z][A-Z0-9_]*$' OR length(v.key)>128 OR jsonb_typeof(v.value)<>'string' OR
  octet_length(v.value #>> '{}')>32768) THEN
  RAISE EXCEPTION 'scoped workload variables require valid keys and string values' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND (SELECT count(*) FROM jsonb_object_keys(NEW.variables))>256 THEN
  RAISE EXCEPTION 'scoped workload variable count exceeds the platform limit' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND ((SELECT count(*) FROM jsonb_object_keys(NEW.service_bindings))>100 OR EXISTS(
  SELECT 1 FROM jsonb_each(NEW.service_bindings) GROUP BY value->>'env_key' HAVING count(*)>1)) THEN
  RAISE EXCEPTION 'scoped service binding count or environment keys are invalid' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND NEW.source->>'kind'='function' AND NOT EXISTS(SELECT 1 FROM apps a
  WHERE a.id=NEW.app_id AND a.type='function' AND a.runtime=NEW.source->>'runtime') THEN
  RAISE EXCEPTION 'function source requires its original supported runner' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND NEW.schedule IS NOT NULL AND NOT EXISTS(SELECT 1 FROM apps a WHERE a.id=NEW.app_id
  AND (NEW.runtime->>'execution_mode'='job' OR a.manifest->>'execution_mode'='job')) THEN
  RAISE EXCEPTION 'scheduled workload requires job execution mode' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.service_bindings) b
  JOIN project_environments e ON e.id=NEW.environment_id WHERE
   EXISTS(SELECT 1 FROM app_envs v WHERE v.app_id=NEW.app_id AND v.scope=e.slug AND v.key=b.value->>'env_key') OR
   EXISTS(SELECT 1 FROM app_environment_secret_refs r WHERE r.app_id=NEW.app_id AND r.environment_id=e.id AND r.key=b.value->>'env_key') OR
   NEW.variables ? (b.value->>'env_key')) THEN
  RAISE EXCEPTION 'scoped service binding environment key is already occupied' USING ERRCODE='23514';
 END IF;
 IF coalesce(cardinality(paths),0)=0 THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=(row_value->>'app_id')::uuid;
  controller:=EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true) AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id
   AND f.environment_id=src.environment_id AND f.resource=resource_name AND f.field_path=ANY(paths)
   AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=f.environment_id
    AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at); END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$_$;

CREATE OR REPLACE FUNCTION public.lifecycle_configuration(app uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object('app',jsonb_build_object('id',a.id,'account',a.account_id,'slug',a.slug,
 'consumer_auth_mode',a.consumer_auth_mode,'maintenance_mode',a.maintenance_mode,
 'manifest',a.manifest,'ram_mb',a.ram_mb,'max_concurrency',a.max_concurrency,'idle_timeout_s',a.idle_timeout_s,'type',a.type,'cpu_millicores',a.cpu_millicores,'scaling_policy',a.scaling_policy,'request_rate_limit_rps',a.request_rate_limit_rps,'request_rate_limit_burst',a.request_rate_limit_burst),
 'account',jsonb_build_object('plan',c.plan,'status',c.status,'past_due_at',c.past_due_at,'abuse_hold_at',c.abuse_hold_at),
 'rules',coalesce((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM edge_rules r WHERE r.app_id=a.id),'[]'::jsonb))
 FROM apps a JOIN accounts c ON c.id=a.account_id WHERE a.id=app
$$;

CREATE OR REPLACE FUNCTION public.service_capacity_snapshot() RETURNS jsonb
    LANGUAGE sql
    SET jit TO 'off'
    AS $$
WITH policy AS (SELECT * FROM service_capacity_policy WHERE singleton),
eligible AS (
    SELECT a.id, a.ram_mb, a.cpu_millicores, a.manifest,
           coalesce((p.plan_vcpus->>ac.plan)::integer, 4) AS vcpu
      FROM apps a JOIN accounts ac ON ac.id=a.account_id CROSS JOIN policy p
     WHERE a.status IN ('active','evicted_cold') AND a.manifest->>'execution_mode'='service'
), scopes AS (
    SELECT id AS app_id, 'default'::text AS scope FROM eligible
    UNION
    SELECT d.app_id, d.scope FROM deployments d JOIN eligible a ON a.id=d.app_id
     WHERE d.status IN ('pending','building','imaging','snapshotting','live')
), declaration AS (
    SELECT s.app_id::text || ':' || s.scope AS key,
           greatest(0, coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) AS desired,
           CASE WHEN EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')
                  AND (EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status IN ('pending','building','imaging','snapshotting'))
                    OR (SELECT count(*) FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')>1)
                THEN 1 ELSE 0 END AS surge,
           a.ram_mb+p.overhead_mb+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS ram,
           greatest(a.cpu_millicores,p.startup_cpu)+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS cpu,
           a.vcpu
      FROM scopes s JOIN eligible a ON a.id=s.app_id CROSS JOIN policy p
), demand AS (
    SELECT key, CASE WHEN desired=0 THEN 0 ELSE desired+surge END AS count, desired, ram, cpu, vcpu
      FROM declaration
), resident AS (
    SELECT i.node_id, a.id::text || ':' || coalesce(d.scope,'default') AS key,
           greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb+coalesce((SELECT sum(greatest(0,(sc->>'ram_mb')::integer))
                           FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS ram,
           greatest(i.capacity_cpu_millicores,greatest(coalesce(a.cpu_millicores,p.startup_cpu),p.startup_cpu)+coalesce((
               SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu)))
                 FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS cpu,
           greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END) AS vcpu,
           (i.state<>'warm' AND i.mode IN ('normal','service') AND a.manifest->>'execution_mode'='service'
              AND EXISTS (SELECT 1 FROM demand x WHERE x.key=a.id::text || ':' || coalesce(d.scope,'default') )) AS service
      FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id
      LEFT JOIN deployments d ON d.id=i.deployment_id CROSS JOIN policy p
     WHERE i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating')
    UNION ALL
    SELECT r.destination_node_id,NULL::text,r.ram_mb::bigint,r.cpu_millicores::bigint,r.vcpu::bigint,false
      FROM migration_admissions r WHERE r.phase NOT IN ('cleaned','committed')
       AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=r.instance_id AND i.node_id=r.destination_node_id AND i.wake_id=r.destination_wake_id AND i.state='running')
    UNION ALL
    SELECT n.id,NULL::text,u.ram,u.cpu,u.vcpu,false FROM compute_nodes n CROSS JOIN LATERAL migration_source_node_usage(n.id) u
      WHERE u.ram>0 OR u.cpu>0 OR u.vcpu>0
), shape AS (
    SELECT greatest(coalesce((SELECT max(ram) FROM demand WHERE count>0),0),coalesce((SELECT max(ram) FROM resident WHERE service),0),1) AS ram,
           greatest(coalesce((SELECT max(cpu) FROM demand WHERE count>0),0),coalesce((SELECT max(cpu) FROM resident WHERE service),0),1) AS cpu,
           greatest(coalesce((SELECT max(vcpu) FROM demand WHERE count>0),0),coalesce((SELECT max(vcpu) FROM resident WHERE service),0),1) AS vcpu
), other_usage AS (
    SELECT node_id, sum(ram)::bigint AS ram, sum(cpu)::bigint AS cpu, sum(vcpu)::bigint AS vcpu
      FROM resident WHERE NOT coalesce(service,false) GROUP BY node_id
), service_usage AS (
    SELECT node_id, count(*)::bigint AS count FROM resident WHERE service GROUP BY node_id
), nodes AS (
    SELECT n.id, n.name, coalesce(u.ram,0) AS other_ram, coalesce(u.cpu,0) AS other_cpu, coalesce(u.vcpu,0) AS other_vcpu,
           coalesce(s.count,0) AS service_count,
           (coalesce(u.ram,0)<=n.admission_ceiling_mb AND coalesce(u.cpu,0)<=n.vpcpus::bigint*1000*p.cpu_overcommit AND coalesce(u.vcpu,0)<=n.vcpu_budget) AS ordinary_fit,
           greatest(0,least((n.admission_ceiling_mb-coalesce(u.ram,0))/sh.ram,
                       (n.vpcpus::bigint*1000*p.cpu_overcommit-coalesce(u.cpu,0))/sh.cpu,
                       (n.vcpu_budget-coalesce(u.vcpu,0))/sh.vcpu))::bigint AS slots
      FROM compute_nodes n CROSS JOIN policy p CROSS JOIN shape sh
      LEFT JOIN other_usage u ON u.node_id=n.id LEFT JOIN service_usage s ON s.node_id=n.id
     WHERE n.lifecycle='active' AND n.admission_ceiling_mb>0 AND n.vpcpus>0 AND n.vcpu_budget>0
       AND n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>p.heartbeat_seconds)
), totals AS (
    SELECT count(*) AS healthy_nodes, coalesce(sum(slots),0) AS fleet_slots,
           coalesce(sum(slots)-max(slots),0) AS failover_slots,
           coalesce(bool_and(service_count<=slots AND ordinary_fit),true) AS placements_fit FROM nodes
), required AS (SELECT coalesce(sum(count),0) AS count, coalesce(sum(desired),0) AS desired FROM demand),
actual_targets AS (SELECT key,count(*) AS count FROM resident WHERE service GROUP BY key),
projection AS (
    SELECT jsonb_build_object(
        'enabled',p.enabled,
        'state',CASE WHEN NOT p.enabled THEN 'disabled'
                     WHEN t.healthy_nodes>=2 AND t.placements_fit AND r.count<=t.failover_slots
                       AND NOT EXISTS (SELECT 1 FROM actual_targets a JOIN demand d ON d.key=a.key WHERE a.count>d.count) THEN 'protected'
                     WHEN t.placements_fit AND r.count<=t.fleet_slots THEN 'degraded' ELSE 'needs_hardware' END,
        'healthy_nodes',t.healthy_nodes,'desired_replicas',r.desired,'reserved_replicas',r.count,
        'replica_ram_mb',sh.ram,'replica_cpu_millicores',sh.cpu,'replica_vcpu',sh.vcpu,
        'fleet_slots',t.fleet_slots,'failover_slots',t.failover_slots,'placements_fit',t.placements_fit,
        'demands',coalesce((SELECT jsonb_object_agg(key,jsonb_build_object('count',count,'ram',ram,'cpu',cpu,'vcpu',vcpu)) FROM demand),'{}'::jsonb),
        'other',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('ram',other_ram,'cpu',other_cpu,'vcpu',other_vcpu)) FROM nodes),'{}'::jsonb),
        'placement',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('slots',slots,'used',service_count)) FROM nodes),'{}'::jsonb),
        'service_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM service_usage),'{}'::jsonb),
        'resident',coalesce((SELECT jsonb_object_agg(node_id::text,cost) FROM (SELECT node_id,jsonb_build_object('ram',sum(ram),'cpu',sum(cpu),'vcpu',sum(vcpu)) AS cost FROM resident GROUP BY node_id) u),'{}'::jsonb),
        'actual',coalesce((SELECT jsonb_object_agg(key,count) FROM actual_targets),'{}'::jsonb)
    ) AS data FROM policy p CROSS JOIN totals t CROSS JOIN required r CROSS JOIN shape sh
) SELECT data FROM projection;
$$;

CREATE OR REPLACE FUNCTION public.workflow_event_recipients(target_account uuid, event_source text, event_type text) RETURNS TABLE(recipient jsonb)
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN accounts ac ON ac.id = a.account_id
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(app_workflow_definitions(a.id,d.workflows)) definition
 WHERE a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;

CREATE OR REPLACE FUNCTION public.workflow_tenant_event_recipients(target_account uuid, target_app uuid, target_tenant uuid, event_source text, event_type text) RETURNS TABLE(recipient jsonb)
    LANGUAGE sql STABLE
    AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.tenant-event:' || a.id::text || ':' || target_tenant::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'platform_tenant_id', target_tenant,
   'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN accounts ac ON ac.id = a.account_id
 JOIN platform_tenants t ON t.id = target_tenant AND t.account_id = a.account_id AND t.status = 'active'
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(app_workflow_definitions(a.id, d.workflows)) definition
 WHERE a.id = target_app AND a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND (
     EXISTS (SELECT 1 FROM api_consumers c WHERE c.account_id = a.account_id AND c.app_id = a.id
       AND c.platform_tenant_id = t.id AND c.status = 'active' AND c.revoked_at IS NULL)
     OR EXISTS (SELECT 1 FROM tenant_surfaces s WHERE s.account_id = a.account_id AND s.app_id = a.id
       AND s.platform_tenant_id = t.id AND s.status = 'active')
   )
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;

CREATE OR REPLACE FUNCTION public.application_standard_runtime_snapshot(application_id uuid, artifact_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE a apps%ROWTYPE; e app_application_standards%ROWTYPE;
        acct accounts%ROWTYPE; o orgs%ROWTYPE; d deployments%ROWTYPE; producers jsonb;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime application is missing' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 SELECT * INTO o FROM orgs WHERE id=a.org_id FOR SHARE NOWAIT;
 SELECT * INTO acct FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 SELECT * INTO e FROM app_application_standards WHERE app_id=a.id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.status='deleted' OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL OR o.status NOT IN ('active','past_due') OR o.deleted_pending
   OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR (e.exception_expires_at IS NOT NULL AND e.exception_expires_at<=clock_timestamp())
   OR NOT ((e.state='unmanaged' AND e.persisted_revision=0 AND e.adoptions='[]'::jsonb AND cardinality(e.materialized_fields)=0)
     OR (e.state IN ('persisted','observed') AND e.persisted_revision=e.desired_revision AND e.effective_hash <> '')) THEN
  RAISE EXCEPTION 'runtime application standards are incomplete' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0)) THEN
  RAISE EXCEPTION 'runtime controls are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 IF artifact_id IS NOT NULL THEN
  SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
  IF NOT FOUND OR d.app_id IS DISTINCT FROM a.id THEN
   RAISE EXCEPTION 'runtime artifact belongs to another application' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
  END IF;
  IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
   RAISE EXCEPTION 'runtime artifact children are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
  END IF;
 END IF;
 IF artifact_id IS NOT NULL THEN producers:=application_standard_runtime_producers(a,d); END IF;
 RETURN application_standard_stable_runtime_input(jsonb_build_object(
  'app_id',a.id::text,'org_id',a.org_id::text,'project_id',coalesce(a.project_id::text,''),'account_id',a.account_id::text,
  'account_plan',acct.plan,'account_egress_allowlist_extra',acct.egress_allowlist_extra,
  'desired_revision',e.desired_revision,'persisted_revision',e.persisted_revision,'effective_hash',e.effective_hash,
  'adoptions',e.adoptions,'materialized_fields',to_jsonb(e.materialized_fields),'effective',e.effective,
  'base_settings',e.base_settings,'local_settings',e.local_settings,'additional_log_destinations',to_jsonb(e.additional_log_destinations::text[]),
  'settings',jsonb_build_object('require_signed',a.require_signed,'security_policy',a.security_policy,
    'egress_cidrs',to_jsonb(a.egress_allowlist::text[]),'egress_extra_ports',to_jsonb(a.egress_ports)),
  'runtime',jsonb_build_object('type',a.type,'runtime',coalesce(a.runtime,''),'ram_mb',a.ram_mb,'app_protocol',a.app_protocol),
  'drains',coalesce((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'kind',r.kind,'enabled',r.enabled,
    'target_hash',encode(sha256(convert_to(r.target_url,'UTF8')),'hex'),
    'auth_hash',encode(sha256(coalesce(r.auth_header_sealed,''::bytea)),'hex')) ORDER BY r.id)
    FROM app_log_drains r WHERE r.app_id=a.id),'[]'::jsonb),
  'signers',coalesce((SELECT jsonb_agg(jsonb_build_object('name',r.signer_name,
    'fingerprint',encode(sha256(r.cosign_public_key),'hex')) ORDER BY r.signer_name)
    FROM app_trusted_signers r WHERE r.app_id=a.id),'[]'::jsonb),
  'artifact',CASE WHEN artifact_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object(
    'id',d.id::text,'scope',d.scope,'kind',d.kind,'image_digest',coalesce(d.image_digest,''),
    'rootfs_key',coalesce(d.rootfs_key,''),'rootfs_path',coalesce(d.rootfs_path,''),'rootfs_bytes',coalesce(d.rootfs_bytes,0),
    'source_sha256',coalesce(d.source_sha256,''),'parked_reason',coalesce(d.parked_reason,''),
    'scan_status',d.scan_status,'scan_result_hash',encode(sha256(convert_to(coalesce(d.scan_result::text,''),'UTF8')),'hex'),
    'sidecars',coalesce((SELECT jsonb_agg(jsonb_build_object('sidecar_name',r.sidecar_name,
      'storage_key',r.storage_key,'bytes',r.bytes,'content_digest',r.content_digest) ORDER BY r.sidecar_name)
      FROM deployment_sidecar_layers r WHERE r.deployment_id=d.id),'[]'::jsonb)) END)
  || CASE WHEN e.exception_expires_at IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('exception_expires_at_unix_nano',(extract(epoch FROM e.exception_expires_at)*1000000000)::bigint) END
  || CASE WHEN producers IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('runtime_artifacts',producers) END);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION public.protect_financial_history() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'DELETE' AND NOT EXISTS(SELECT 1 FROM accounts WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
  IF TG_TABLE_NAME = 'financial_price_snapshots' AND TG_OP = 'UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'financial history is append-only';
END $$;

CREATE OR REPLACE FUNCTION public.protect_financial_budget_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  -- Account removal may erase its financial data; ordinary policy deletion
  -- is a revisioned tombstone and never removes the audit trail.
  IF TG_OP = 'DELETE' AND NOT EXISTS(SELECT 1 FROM accounts WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'financial budget revisions are append-only';
END $$;
DROP TRIGGER org_app_resource_policy ON orgs;
DROP FUNCTION notify_org_app_resource_policy();
DROP FUNCTION lock_app_resource_account(uuid,boolean,boolean);
DROP VIEW app_resource_accounts;
DROP TRIGGER accounts_shared_app_deletion_transition ON accounts;
DROP FUNCTION guard_account_shared_app_deletion_transition();
CREATE OR REPLACE FUNCTION guard_account_shared_app_purge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id WHERE a.account_id=OLD.id AND NOT o.personal_org) THEN
        RAISE EXCEPTION 'shared app ownership migration required before account purge'
            USING ERRCODE='23514', CONSTRAINT='account_shared_app_ownership_pending';
    END IF;
    RETURN OLD;
END $$;
DROP FUNCTION org_app_identity_erasure_blocked(uuid);
ALTER TABLE stripe_push_dedupe DROP CONSTRAINT stripe_push_dedupe_account_id_fkey;
ALTER TABLE stripe_push_dedupe ADD CONSTRAINT stripe_push_dedupe_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE request_id_journal DROP CONSTRAINT request_id_journal_account_id_fkey;
ALTER TABLE request_id_journal ADD CONSTRAINT request_id_journal_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE request_audit_events DROP CONSTRAINT request_audit_events_account_id_fkey;
ALTER TABLE request_audit_events ADD CONSTRAINT request_audit_events_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE paddle_overage_dedupe DROP CONSTRAINT paddle_overage_dedupe_account_id_fkey;
ALTER TABLE paddle_overage_dedupe ADD CONSTRAINT paddle_overage_dedupe_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE invoices DROP CONSTRAINT invoices_account_id_fkey;
ALTER TABLE invoices ADD CONSTRAINT invoices_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE invocations DROP CONSTRAINT invocations_account_id_fkey;
ALTER TABLE invocations ADD CONSTRAINT invocations_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);
ALTER TABLE financial_usage_evidence DROP CONSTRAINT financial_usage_evidence_account_id_fkey;
ALTER TABLE financial_usage_evidence ADD CONSTRAINT financial_usage_evidence_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE financial_price_snapshots DROP CONSTRAINT financial_price_snapshots_account_id_fkey;
ALTER TABLE financial_price_snapshots ADD CONSTRAINT financial_price_snapshots_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE financial_budget_policies DROP CONSTRAINT financial_budget_policies_account_id_fkey;
ALTER TABLE financial_budget_policies ADD CONSTRAINT financial_budget_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE deployment_scope_exclusions DROP CONSTRAINT deployment_scope_exclusions_account_id_fkey;
ALTER TABLE deployment_scope_exclusions ADD CONSTRAINT deployment_scope_exclusions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE deployment_openapi_docs DROP CONSTRAINT deployment_openapi_docs_account_id_fkey;
ALTER TABLE deployment_openapi_docs ADD CONSTRAINT deployment_openapi_docs_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_account_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE billing_usage_deliveries DROP CONSTRAINT billing_usage_deliveries_account_id_fkey;
ALTER TABLE billing_usage_deliveries ADD CONSTRAINT billing_usage_deliveries_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE billing_meter_usage_deliveries DROP CONSTRAINT billing_meter_usage_deliveries_account_id_fkey;
ALTER TABLE billing_meter_usage_deliveries ADD CONSTRAINT billing_meter_usage_deliveries_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE billing_identities DROP CONSTRAINT billing_identities_account_id_fkey;
ALTER TABLE billing_identities ADD CONSTRAINT billing_identities_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE apps DROP CONSTRAINT apps_account_id_fkey;
ALTER TABLE apps ADD CONSTRAINT apps_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);
ALTER TABLE app_trusted_signers DROP CONSTRAINT app_trusted_signers_account_id_fkey;
ALTER TABLE app_trusted_signers ADD CONSTRAINT app_trusted_signers_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_account_id_fkey;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_service_wake_ahead DROP CONSTRAINT app_service_wake_ahead_account_id_fkey;
ALTER TABLE app_service_wake_ahead ADD CONSTRAINT app_service_wake_ahead_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_secrets DROP CONSTRAINT app_secrets_account_id_fkey;
ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_secret_revocations DROP CONSTRAINT app_secret_revocations_account_id_fkey;
ALTER TABLE app_secret_revocations ADD CONSTRAINT app_secret_revocations_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_registry_credentials DROP CONSTRAINT app_registry_credentials_account_id_fkey;
ALTER TABLE app_registry_credentials ADD CONSTRAINT app_registry_credentials_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_openapi_docs DROP CONSTRAINT app_openapi_docs_account_id_fkey;
ALTER TABLE app_openapi_docs ADD CONSTRAINT app_openapi_docs_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_log_drains DROP CONSTRAINT app_log_drains_account_id_fkey;
ALTER TABLE app_log_drains ADD CONSTRAINT app_log_drains_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_health_history DROP CONSTRAINT app_health_history_account_id_fkey;
ALTER TABLE app_health_history ADD CONSTRAINT app_health_history_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_health_collection_state DROP CONSTRAINT app_health_collection_state_account_id_fkey;
ALTER TABLE app_health_collection_state ADD CONSTRAINT app_health_collection_state_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_errors DROP CONSTRAINT app_errors_account_id_fkey;
ALTER TABLE app_errors ADD CONSTRAINT app_errors_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_error_requests DROP CONSTRAINT app_error_requests_account_id_fkey;
ALTER TABLE app_error_requests ADD CONSTRAINT app_error_requests_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE app_envs DROP CONSTRAINT app_envs_account_id_fkey;
ALTER TABLE app_envs ADD CONSTRAINT app_envs_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE account_spend_snapshot DROP CONSTRAINT account_spend_snapshot_account_id_fkey;
ALTER TABLE account_spend_snapshot ADD CONSTRAINT account_spend_snapshot_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE account_credits DROP CONSTRAINT account_credits_account_id_fkey;
ALTER TABLE account_credits ADD CONSTRAINT account_credits_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE orgs DROP COLUMN resource_ownership_enabled, DROP COLUMN past_due_at, DROP COLUMN last_quota_warning_at,
    DROP COLUMN suspended_reason, DROP COLUMN abuse_hold_at, DROP COLUMN abuse_hold_reason,
    DROP COLUMN billing_email, DROP COLUMN business_name, DROP COLUMN billing_address, DROP COLUMN tax_id;
ALTER TABLE account_async_quota DROP CONSTRAINT account_async_quota_account_id_fkey;
ALTER TABLE account_async_quota ADD CONSTRAINT account_async_quota_account_id_fkey
 FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;

DROP TRIGGER org_billing_identity ON billing_identities;
DROP FUNCTION activate_org_billing_identity();
DROP TRIGGER org_resource_ownership ON orgs;
DROP FUNCTION activate_org_resource_ownership();
DROP TRIGGER org_resource_namespace ON orgs;
DROP TRIGGER account_resource_namespace ON accounts;
DROP FUNCTION seed_resource_namespace();
DROP TABLE resource_namespaces;
-- +goose StatementEnd
RESET lock_timeout;
