-- +goose Up
SET LOCAL lock_timeout = '5s';
-- migration-lint: allow not-null queue_bindings.org_id: bind_queue_organization derives organization ownership from the existing app_id for legacy INSERTs after validated backfill.
-- migration-lint: allow index-build queue_bindings: Atomic binding ownership cutover with admission/dispatch and metadata writers paused; production sizing/timing qualification is required (ADR-202610171908).
-- migration-lint: allow index-build invocations: Reservation subjects and their index are installed in one atomic counter cutover with every quota writer quiesced; production sizing/timing qualification is required (ADR-202610171908).
-- Replay completed expansion without replacing functions refined by later migrations.
-- A partially present expansion still fails atomically and requires repair.
-- +goose StatementBegin
DO $org_ownership_replay$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('queue_bindings') AND attname='org_id' AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('queue_bindings') AND conname='queue_bindings_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('project_environment_queue_runtime_sets') AND conname='project_environment_queue_runtime_sets_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('invocation_environment_queue_admissions') AND conname='invocation_environment_queue_admissions_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('triggers') AND conname='triggers_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('dead_letter_events') AND conname='dead_letter_events_account_id_fkey')
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('invocations') AND attname='async_quota_subject_id' AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('invocations') AND conname='invocation_async_subject_required')
    AND to_regclass('queue_bindings_org_id_idx') IS NOT NULL
    AND to_regclass('invocation_async_subject_reserved_idx') IS NOT NULL
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='bind_queue_organization')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='invocation_async_subject')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='stamp_invocation_async_subject')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='adopt_org_async_reservations')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='adopt_queue_reservations_on_org_adoption')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_org_queue_bindings')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='org_app_identity_erasure_blocked')
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('queue_bindings') AND tgname='queue_organization' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('invocations') AND tgname='invocation_async_subject' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('orgs') AND tgname='org_queue_reservation_adoption' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('orgs') AND tgname='org_queue_bindings_guard' AND NOT tgisinternal) THEN
  RETURN;
 END IF;

-- ADR-202610171908: durable queue ownership without moving storage or accepted work.
ALTER TABLE queue_bindings ADD COLUMN org_id uuid REFERENCES orgs(id);
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM queue_bindings b JOIN apps a ON a.id=b.app_id
   WHERE b.account_id<>a.account_id OR a.org_id IS NULL)
 OR EXISTS(SELECT 1 FROM dead_letter_events d JOIN apps a ON a.id=d.app_id WHERE d.account_id<>a.account_id)
 OR EXISTS(SELECT 1 FROM triggers t JOIN apps a ON a.id=t.app_id WHERE t.account_id<>a.account_id)
 OR EXISTS(SELECT 1 FROM project_environment_queue_runtime_sets q JOIN apps a ON a.id=q.app_id
   JOIN projects p ON p.id=q.project_id WHERE q.account_id<>a.account_id OR q.account_id<>p.account_id OR a.org_id IS DISTINCT FROM p.org_id)
 OR EXISTS(SELECT 1 FROM invocation_environment_queue_admissions q JOIN invocations i ON i.id=q.invocation_id
   JOIN apps a ON a.id=q.app_id WHERE q.account_id<>a.account_id OR i.account_id<>q.account_id OR i.app_id<>q.app_id) THEN
  RAISE EXCEPTION 'remediate mixed queue ownership roots before organization migration';
 END IF;
END $$;
UPDATE queue_bindings b SET org_id=a.org_id FROM apps a WHERE a.id=b.app_id;
ALTER TABLE queue_bindings ALTER COLUMN org_id SET NOT NULL;
CREATE INDEX queue_bindings_org_id_idx ON queue_bindings(org_id);
CREATE FUNCTION bind_queue_organization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_org uuid; namespace uuid;
BEGIN
 SELECT org_id,account_id INTO owner_org,namespace FROM apps WHERE id=NEW.app_id FOR SHARE;
 IF NOT FOUND OR owner_org IS NULL OR namespace IS DISTINCT FROM NEW.account_id
   OR (NEW.org_id IS NOT NULL AND NEW.org_id IS DISTINCT FROM owner_org) THEN
  RAISE EXCEPTION 'queue binding organization does not match its app' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id,NEW.app_id,NEW.org_id) IS DISTINCT FROM (OLD.account_id,OLD.app_id,OLD.org_id) THEN
  RAISE EXCEPTION 'queue binding ownership is immutable' USING ERRCODE='23514';
 END IF;
 NEW.org_id:=owner_org;
 RETURN NEW;
END $$;
CREATE TRIGGER queue_organization BEFORE INSERT OR UPDATE ON queue_bindings FOR EACH ROW EXECUTE FUNCTION bind_queue_organization();
ALTER TABLE queue_bindings DROP CONSTRAINT queue_bindings_account_id_fkey;
ALTER TABLE queue_bindings ADD CONSTRAINT queue_bindings_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE project_environment_queue_runtime_sets DROP CONSTRAINT project_environment_queue_runtime_sets_account_id_fkey;
ALTER TABLE project_environment_queue_runtime_sets ADD CONSTRAINT project_environment_queue_runtime_sets_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;
ALTER TABLE invocation_environment_queue_admissions DROP CONSTRAINT invocation_environment_queue_admissions_account_id_fkey;
ALTER TABLE invocation_environment_queue_admissions ADD CONSTRAINT invocation_environment_queue_admissions_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id);
ALTER TABLE triggers DROP CONSTRAINT triggers_account_id_fkey;
ALTER TABLE triggers ADD CONSTRAINT triggers_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;

ALTER TABLE dead_letter_events DROP CONSTRAINT dead_letter_events_account_id_fkey;
ALTER TABLE dead_letter_events ADD CONSTRAINT dead_letter_events_account_id_fkey FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE;

ALTER TABLE invocations ADD COLUMN async_quota_subject_id uuid REFERENCES resource_namespaces(id);
UPDATE invocations SET async_quota_subject_id=account_id WHERE quota_reserved;
ALTER TABLE invocations ADD CONSTRAINT invocation_async_subject_required CHECK(NOT quota_reserved OR async_quota_subject_id IS NOT NULL);
CREATE INDEX invocation_async_subject_reserved_idx ON invocations(async_quota_subject_id) WHERE quota_reserved;
CREATE FUNCTION invocation_async_subject(app uuid,namespace uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE a.account_id END
 FROM apps a JOIN orgs o ON o.id=a.org_id WHERE a.id=app AND a.account_id=namespace;
$$;
CREATE FUNCTION stamp_invocation_async_subject() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected uuid;
BEGIN
 IF NEW.quota_reserved THEN
  expected:=invocation_async_subject(NEW.app_id,NEW.account_id);
  IF expected IS NULL THEN RAISE EXCEPTION 'invalid invocation quota ownership' USING ERRCODE='23514'; END IF;
  IF TG_OP='INSERT' OR NOT OLD.quota_reserved THEN
   NEW.async_quota_subject_id:=expected;
  ELSIF NEW.async_quota_subject_id IS DISTINCT FROM OLD.async_quota_subject_id AND NEW.async_quota_subject_id IS DISTINCT FROM expected THEN
   RAISE EXCEPTION 'invocation quota reservation ownership is fenced' USING ERRCODE='23514';
  END IF;
 ELSIF TG_OP='UPDATE' AND OLD.quota_reserved THEN
  NEW.async_quota_subject_id:=OLD.async_quota_subject_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER invocation_async_subject BEFORE INSERT OR UPDATE ON invocations FOR EACH ROW EXECUTE FUNCTION stamp_invocation_async_subject();
CREATE FUNCTION adopt_org_async_reservations(company uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE moved record; total bigint:=0; changed integer;
BEGIN
 PERFORM id FROM orgs WHERE id=company AND NOT personal_org AND resource_ownership_enabled FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 -- Lock accepted work before counters; completion has the same row-before-counter order.
 PERFORM i.id FROM invocations i JOIN apps a ON a.id=i.app_id WHERE a.org_id=company
   AND i.quota_reserved AND i.async_quota_subject_id<>company ORDER BY i.id FOR UPDATE OF i;
 FOR moved IN SELECT i.async_quota_subject_id AS subject,count(*) AS amount
  FROM invocations i JOIN apps a ON a.id=i.app_id WHERE a.org_id=company AND i.quota_reserved
  AND i.async_quota_subject_id<>company GROUP BY i.async_quota_subject_id ORDER BY i.async_quota_subject_id LOOP
  UPDATE account_async_quota SET current_inflight=current_inflight-moved.amount,updated_at=now()
   WHERE account_id=moved.subject AND current_inflight>=moved.amount;
  GET DIAGNOSTICS changed=ROW_COUNT;
  IF changed<>1 THEN RAISE EXCEPTION 'remediate inconsistent async reservation counters before organization adoption'; END IF;
  total:=total+moved.amount;
 END LOOP;
 IF total>0 THEN
  -- A compatible claimant installs the current api.Limits plan cap before admission.
  INSERT INTO account_async_quota(account_id,max_inflight,current_inflight) VALUES(company,0,total)
   ON CONFLICT(account_id) DO UPDATE SET current_inflight=account_async_quota.current_inflight+EXCLUDED.current_inflight,updated_at=now();
  UPDATE invocations i SET async_quota_subject_id=company FROM apps a
   WHERE a.id=i.app_id AND a.org_id=company AND i.quota_reserved AND i.async_quota_subject_id<>company;
 END IF;
END $$;
DO $$ DECLARE company uuid; BEGIN
 FOR company IN SELECT id FROM orgs WHERE NOT personal_org AND resource_ownership_enabled ORDER BY id LOOP
  PERFORM adopt_org_async_reservations(company);
 END LOOP;
END $$;
CREATE FUNCTION adopt_queue_reservations_on_org_adoption() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.resource_ownership_enabled AND NOT OLD.resource_ownership_enabled THEN PERFORM adopt_org_async_reservations(NEW.id); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER org_queue_reservation_adoption AFTER UPDATE OF resource_ownership_enabled ON orgs
 FOR EACH ROW EXECUTE FUNCTION adopt_queue_reservations_on_org_adoption();
CREATE FUNCTION guard_org_queue_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.deleted_pending OR NEW.status='deleted_pending') AND EXISTS
  (SELECT 1 FROM queue_bindings b WHERE b.org_id=NEW.id AND b.retired_at IS NULL) THEN
  RAISE EXCEPTION 'retire organization queue bindings before deletion' USING ERRCODE='23514',CONSTRAINT='org_has_queue_bindings';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER org_queue_bindings_guard BEFORE UPDATE OF status,deleted_pending ON orgs
 FOR EACH ROW EXECUTE FUNCTION guard_org_queue_bindings();
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
    IF EXISTS (SELECT 1 FROM triggers t JOIN apps a ON a.id=t.app_id JOIN orgs o ON o.id=a.org_id
       WHERE t.account_id=identity_id AND NOT o.personal_org AND NOT
       (t.kind='queue' AND t.source='queue' AND EXISTS(SELECT 1 FROM queue_bindings b
        WHERE b.id=t.queue_binding_id AND b.app_id=t.app_id AND b.account_id=t.account_id AND b.org_id=a.org_id))) THEN
      RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM dead_letter_events d LEFT JOIN apps a ON a.id=d.app_id
       LEFT JOIN orgs o ON o.id=a.org_id WHERE d.account_id=identity_id AND (a.id IS NULL OR
       (NOT o.personal_org AND NOT (
        (d.source='invocation' AND (d.origin='queue' OR EXISTS(SELECT 1 FROM invocations i WHERE i.id=d.source_id AND i.source='queue' AND i.app_id=d.app_id)))
        OR (d.source='trigger_record' AND EXISTS(SELECT 1 FROM triggers t JOIN queue_bindings b ON b.id=t.queue_binding_id
          WHERE t.id=d.trigger_id AND t.app_id=d.app_id AND t.account_id=d.account_id AND t.kind='queue' AND t.source='queue')))))) THEN
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
        'scenario_postgres_fixtures',
        'scenario_suspend_resumes',
        'scenario_test_action_policies',
        'scenario_test_chaos_matches',
        'scenario_test_members',
        'scenario_worker_faults',
        'schedule_occurrences',
        'tenant_surfaces',
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
END;
$org_ownership_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Reservations cannot change counter ownership during a rollback.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM invocations WHERE quota_reserved AND async_quota_subject_id<>account_id) THEN
  RAISE EXCEPTION 'drain organization async reservations before rolling back queue ownership';
 END IF;
END $$;
DROP TRIGGER org_queue_bindings_guard ON orgs;
DROP FUNCTION guard_org_queue_bindings();
DROP TRIGGER org_queue_reservation_adoption ON orgs;
DROP FUNCTION adopt_queue_reservations_on_org_adoption();
DROP FUNCTION adopt_org_async_reservations(uuid);
DROP TRIGGER invocation_async_subject ON invocations;
DROP FUNCTION stamp_invocation_async_subject();
DROP FUNCTION invocation_async_subject(uuid,uuid);
ALTER TABLE invocations DROP COLUMN async_quota_subject_id;
DROP TRIGGER queue_organization ON queue_bindings;
DROP FUNCTION bind_queue_organization();
ALTER TABLE queue_bindings DROP COLUMN org_id;
ALTER TABLE queue_bindings DROP CONSTRAINT queue_bindings_account_id_fkey;
ALTER TABLE queue_bindings ADD CONSTRAINT queue_bindings_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE project_environment_queue_runtime_sets DROP CONSTRAINT project_environment_queue_runtime_sets_account_id_fkey;
ALTER TABLE project_environment_queue_runtime_sets ADD CONSTRAINT project_environment_queue_runtime_sets_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE invocation_environment_queue_admissions DROP CONSTRAINT invocation_environment_queue_admissions_account_id_fkey;
ALTER TABLE invocation_environment_queue_admissions ADD CONSTRAINT invocation_environment_queue_admissions_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id);
ALTER TABLE dead_letter_events DROP CONSTRAINT dead_letter_events_account_id_fkey;
ALTER TABLE dead_letter_events ADD CONSTRAINT dead_letter_events_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE triggers DROP CONSTRAINT triggers_account_id_fkey;
ALTER TABLE triggers ADD CONSTRAINT triggers_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
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
