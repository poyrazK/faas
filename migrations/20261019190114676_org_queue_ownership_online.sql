-- migration-compat: contract
-- Organization-owned queues and worker bindings (ADR-202610171908), applied online (ADR-202610171900, ADR-202610171908).
-- Replaces the single-transaction 20261018003100007, which never ran in production.
--
-- Each step is its own short transaction on at most one existing table (plus
-- the table a new foreign key references), waits at most 500ms for a lock
-- (below deadlock_timeout, so the migration is the side that yields) and
-- retries. Backfills commit every batch. Constraints are added NOT VALID and
-- validated separately. Every step is idempotent.
--
-- Async reservations are captured on each invocation and keep using the
-- storage namespace's counter: rc.272 quota writers release by namespace,
-- so no reservation moves to a company counter in this release.
-- migration-lint: allow not-null queue_bindings.org_id: bind_queue_organization derives organization ownership from the existing app_id for legacy INSERTs after the validated backfill.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '500ms';

-- ---------------------------------------------------------------- 1. new nullable columns
-- +goose StatementBegin
-- queue_bindings: org_id column
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE queue_bindings ADD COLUMN IF NOT EXISTS org_id uuid;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: org_id column' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: async_quota_subject_id column
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE invocations ADD COLUMN IF NOT EXISTS async_quota_subject_id uuid;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: async_quota_subject_id column' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: async_quota_pooled_at column (NULL: reservations stay on the namespace counter)
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE orgs ADD COLUMN IF NOT EXISTS async_quota_pooled_at timestamptz;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: async_quota_pooled_at column (NULL: reservations stay on the namespace counter)' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 2. functions
-- bind_queue_organization
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_queue_organization() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementEnd
-- invocation_async_subject
-- +goose StatementBegin
-- Async reservations stay on the storage namespace's counter, which every
-- release since ADR-134 increments and decrements, until the organization's
-- reservations are pooled by adopt_org_async_reservations. Pooling needs every
-- quota writer to release the captured subject, so it runs only in the
-- release after the one that adds the capture (ADR-202610171908).
CREATE OR REPLACE FUNCTION invocation_async_subject(app uuid,namespace uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN o.id IS NOT NULL AND NOT o.personal_org AND o.resource_ownership_enabled
             AND o.async_quota_pooled_at IS NOT NULL THEN o.id ELSE a.account_id END
 FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.id=app AND a.account_id=namespace;
$$;
-- +goose StatementEnd
-- stamp_invocation_async_subject
-- +goose StatementBegin
-- A reservation without a resolvable app keeps the legacy namespace subject:
-- that is the counter the claimant incremented.
CREATE OR REPLACE FUNCTION stamp_invocation_async_subject() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected uuid;
BEGIN
 IF NEW.quota_reserved THEN
  expected:=coalesce(invocation_async_subject(NEW.app_id,NEW.account_id),NEW.account_id);
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
-- +goose StatementEnd
-- adopt_org_async_reservations
-- +goose StatementBegin
-- Pool a company's async reservations on its own counter. Marks the
-- organization first so the subject fence accepts the moved reservations,
-- then moves accepted work before counters (completion locks the same way).
-- Not called by this release; see invocation_async_subject.
CREATE OR REPLACE FUNCTION adopt_org_async_reservations(company uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE moved record; total bigint:=0; changed integer;
BEGIN
 PERFORM id FROM orgs WHERE id=company AND NOT personal_org AND resource_ownership_enabled FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 UPDATE orgs SET async_quota_pooled_at=now() WHERE id=company AND async_quota_pooled_at IS NULL;
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
-- +goose StatementEnd
-- guard_org_queue_bindings
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_org_queue_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.deleted_pending OR NEW.status='deleted_pending') AND EXISTS
  (SELECT 1 FROM queue_bindings b WHERE b.org_id=NEW.id AND b.retired_at IS NULL) THEN
  RAISE EXCEPTION 'retire organization queue bindings before deletion' USING ERRCODE='23514',CONSTRAINT='org_has_queue_bindings';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: no reservation move on adoption in this release (left by an earlier draft)
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   DROP TRIGGER IF EXISTS org_queue_reservation_adoption ON orgs;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: no reservation move on adoption in this release (left by an earlier draft)' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- adopt_queue_reservations_on_org_adoption: unused
DROP FUNCTION IF EXISTS adopt_queue_reservations_on_org_adoption();

-- ---------------------------------------------------------------- 3. triggers that derive the new fields for legacy writers
-- +goose StatementBegin
-- queue_bindings: trigger queue_organization
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER queue_organization BEFORE INSERT OR UPDATE ON queue_bindings FOR EACH ROW EXECUTE FUNCTION bind_queue_organization();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: trigger queue_organization' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: trigger invocation_async_subject
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER invocation_async_subject BEFORE INSERT OR UPDATE ON invocations FOR EACH ROW EXECUTE FUNCTION stamp_invocation_async_subject();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: trigger invocation_async_subject' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 4. refuse mixed roots, then backfill in batches
-- queues: refuse mixed ownership roots before changing anything
-- +goose StatementBegin
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
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings: org_id derived from the app by the ownership trigger
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM queue_bindings WHERE org_id IS NULL AND id > last ORDER BY id LIMIT 500) batch;
   IF ids IS NOT NULL THEN
    UPDATE queue_bindings SET org_id = NULL WHERE id = ANY (ids) AND org_id IS NULL;
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
-- invocation_async_subject_reserved_idx: drop an interrupted build
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('invocation_async_subject_reserved_idx') AND NOT indisvalid) THEN
    DROP INDEX invocation_async_subject_reserved_idx;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocation_async_subject_reserved_idx: drop an interrupted build' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- invocation_async_subject_reserved_idx: build concurrently (waits only for older transactions)
SET lock_timeout = '10s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocation_async_subject_reserved_idx ON invocations (async_quota_subject_id) WHERE quota_reserved;
SET lock_timeout = '500ms';
-- +goose StatementBegin
-- invocations: open reservations capture the namespace counter they hold
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM invocations WHERE quota_reserved AND async_quota_subject_id IS NULL AND id > last ORDER BY id LIMIT 500) batch;
   IF ids IS NOT NULL THEN
    UPDATE invocations SET async_quota_subject_id = account_id WHERE id = ANY (ids) AND quota_reserved AND async_quota_subject_id IS NULL;
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
-- orgs: keep pooling where an earlier draft already moved reservations
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT o.id FROM orgs o WHERE o.async_quota_pooled_at IS NULL AND NOT o.personal_org AND o.resource_ownership_enabled AND EXISTS (SELECT 1 FROM invocations i WHERE i.quota_reserved AND i.async_quota_subject_id = o.id) AND o.id > last ORDER BY o.id LIMIT 100) batch;
   IF ids IS NOT NULL THEN
    UPDATE orgs SET async_quota_pooled_at = now() WHERE id = ANY (ids) AND async_quota_pooled_at IS NULL;
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

-- ---------------------------------------------------------------- 5. guards that read the backfilled columns
-- +goose StatementBegin
-- orgs: trigger org_queue_bindings_guard
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_queue_bindings_guard BEFORE UPDATE OF status,deleted_pending ON orgs
    FOR EACH ROW EXECUTE FUNCTION guard_org_queue_bindings();
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_queue_bindings_guard' USING ERRCODE = '55P03';
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
        'scenario_postgres_faults',
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
-- +goose StatementEnd

-- ---------------------------------------------------------------- 6. constraints NOT VALID
-- +goose StatementBegin
-- queue_bindings: add queue_bindings_org_id_fkey NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'queue_bindings_org_id_fkey') THEN
    ALTER TABLE queue_bindings ADD CONSTRAINT queue_bindings_org_id_fkey FOREIGN KEY (org_id) REFERENCES orgs(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: add queue_bindings_org_id_fkey NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: add invocations_async_quota_subject_id_fkey NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'invocations_async_quota_subject_id_fkey') THEN
    ALTER TABLE invocations ADD CONSTRAINT invocations_async_quota_subject_id_fkey FOREIGN KEY (async_quota_subject_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: add invocations_async_quota_subject_id_fkey NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: add invocation_async_subject_required NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'invocation_async_subject_required') THEN
    ALTER TABLE invocations ADD CONSTRAINT invocation_async_subject_required CHECK (NOT quota_reserved OR async_quota_subject_id IS NOT NULL) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: add invocation_async_subject_required NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings.org_id: prove NOT NULL with a NOT VALID check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'queue_bindings'::regclass AND attname = 'org_id' AND NOT attnotnull) AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'org_nn_942d7951174a') THEN
    ALTER TABLE queue_bindings ADD CONSTRAINT org_nn_942d7951174a CHECK (org_id IS NOT NULL) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings.org_id: prove NOT NULL with a NOT VALID check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'queue_bindings_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'org_ns_fk_458e5f8ad3c5') THEN
    ALTER TABLE queue_bindings ADD CONSTRAINT org_ns_fk_458e5f8ad3c5 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_queue_runtime_sets: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_queue_runtime_sets'::regclass AND conname = 'project_environment_queue_runtime_sets_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_queue_runtime_sets'::regclass AND conname = 'org_ns_fk_85a74f54cd5e') THEN
    ALTER TABLE project_environment_queue_runtime_sets ADD CONSTRAINT org_ns_fk_85a74f54cd5e FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_queue_runtime_sets: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocation_environment_queue_admissions: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_environment_queue_admissions'::regclass AND conname = 'invocation_environment_queue_admissions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_environment_queue_admissions'::regclass AND conname = 'org_ns_fk_06724afb6e44') THEN
    ALTER TABLE invocation_environment_queue_admissions ADD CONSTRAINT org_ns_fk_06724afb6e44 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocation_environment_queue_admissions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- triggers: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'triggers'::regclass AND conname = 'triggers_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'triggers'::regclass AND conname = 'org_ns_fk_50abf9b68b72') THEN
    ALTER TABLE triggers ADD CONSTRAINT org_ns_fk_50abf9b68b72 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: triggers: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- dead_letter_events: add the namespace foreign key NOT VALID
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dead_letter_events'::regclass AND conname = 'dead_letter_events_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dead_letter_events'::regclass AND conname = 'org_ns_fk_1768a8a87496') THEN
    ALTER TABLE dead_letter_events ADD CONSTRAINT org_ns_fk_1768a8a87496 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: dead_letter_events: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 7. validate
-- +goose StatementBegin
-- queue_bindings: validate queue_bindings_org_id_fkey
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'queue_bindings_org_id_fkey' AND NOT convalidated) THEN
    ALTER TABLE queue_bindings VALIDATE CONSTRAINT queue_bindings_org_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: validate queue_bindings_org_id_fkey' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: validate invocations_async_quota_subject_id_fkey
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'invocations_async_quota_subject_id_fkey' AND NOT convalidated) THEN
    ALTER TABLE invocations VALIDATE CONSTRAINT invocations_async_quota_subject_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: validate invocations_async_quota_subject_id_fkey' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocations: validate invocation_async_subject_required
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'invocation_async_subject_required' AND NOT convalidated) THEN
    ALTER TABLE invocations VALIDATE CONSTRAINT invocation_async_subject_required;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocations: validate invocation_async_subject_required' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings: validate org_nn_942d7951174a
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'org_nn_942d7951174a' AND NOT convalidated) THEN
    ALTER TABLE queue_bindings VALIDATE CONSTRAINT org_nn_942d7951174a;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: validate org_nn_942d7951174a' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'org_ns_fk_458e5f8ad3c5' AND NOT convalidated) THEN
    ALTER TABLE queue_bindings VALIDATE CONSTRAINT org_ns_fk_458e5f8ad3c5;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_queue_runtime_sets: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_queue_runtime_sets'::regclass AND conname = 'org_ns_fk_85a74f54cd5e' AND NOT convalidated) THEN
    ALTER TABLE project_environment_queue_runtime_sets VALIDATE CONSTRAINT org_ns_fk_85a74f54cd5e;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_queue_runtime_sets: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocation_environment_queue_admissions: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_environment_queue_admissions'::regclass AND conname = 'org_ns_fk_06724afb6e44' AND NOT convalidated) THEN
    ALTER TABLE invocation_environment_queue_admissions VALIDATE CONSTRAINT org_ns_fk_06724afb6e44;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocation_environment_queue_admissions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- triggers: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'triggers'::regclass AND conname = 'org_ns_fk_50abf9b68b72' AND NOT convalidated) THEN
    ALTER TABLE triggers VALIDATE CONSTRAINT org_ns_fk_50abf9b68b72;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: triggers: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- dead_letter_events: validate the namespace foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dead_letter_events'::regclass AND conname = 'org_ns_fk_1768a8a87496' AND NOT convalidated) THEN
    ALTER TABLE dead_letter_events VALIDATE CONSTRAINT org_ns_fk_1768a8a87496;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: dead_letter_events: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 8. retire replaced constraints, tighten, index
-- +goose StatementBegin
-- queue_bindings: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'queue_bindings'::regclass AND conname = 'org_ns_fk_458e5f8ad3c5') THEN
    ALTER TABLE queue_bindings DROP CONSTRAINT IF EXISTS queue_bindings_account_id_fkey;
    ALTER TABLE queue_bindings RENAME CONSTRAINT org_ns_fk_458e5f8ad3c5 TO queue_bindings_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- project_environment_queue_runtime_sets: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_queue_runtime_sets'::regclass AND conname = 'org_ns_fk_85a74f54cd5e') THEN
    ALTER TABLE project_environment_queue_runtime_sets DROP CONSTRAINT IF EXISTS project_environment_queue_runtime_sets_account_id_fkey;
    ALTER TABLE project_environment_queue_runtime_sets RENAME CONSTRAINT org_ns_fk_85a74f54cd5e TO project_environment_queue_runtime_sets_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environment_queue_runtime_sets: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- invocation_environment_queue_admissions: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocation_environment_queue_admissions'::regclass AND conname = 'org_ns_fk_06724afb6e44') THEN
    ALTER TABLE invocation_environment_queue_admissions DROP CONSTRAINT IF EXISTS invocation_environment_queue_admissions_account_id_fkey;
    ALTER TABLE invocation_environment_queue_admissions RENAME CONSTRAINT org_ns_fk_06724afb6e44 TO invocation_environment_queue_admissions_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: invocation_environment_queue_admissions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- triggers: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'triggers'::regclass AND conname = 'org_ns_fk_50abf9b68b72') THEN
    ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_account_id_fkey;
    ALTER TABLE triggers RENAME CONSTRAINT org_ns_fk_50abf9b68b72 TO triggers_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: triggers: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- dead_letter_events: retire the accounts foreign key
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dead_letter_events'::regclass AND conname = 'org_ns_fk_1768a8a87496') THEN
    ALTER TABLE dead_letter_events DROP CONSTRAINT IF EXISTS dead_letter_events_account_id_fkey;
    ALTER TABLE dead_letter_events RENAME CONSTRAINT org_ns_fk_1768a8a87496 TO dead_letter_events_account_id_fkey;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: dead_letter_events: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings.org_id: SET NOT NULL from the validated check
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'queue_bindings'::regclass AND attname = 'org_id' AND NOT attnotnull) THEN
    ALTER TABLE queue_bindings ALTER COLUMN org_id SET NOT NULL;
   END IF;
   ALTER TABLE queue_bindings DROP CONSTRAINT IF EXISTS org_nn_942d7951174a;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings.org_id: SET NOT NULL from the validated check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- queue_bindings_org_id_idx: drop an interrupted build
DO $online$
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('queue_bindings_org_id_idx') AND NOT indisvalid) THEN
    DROP INDEX queue_bindings_org_id_idx;
   END IF;
   PERFORM set_config('lock_timeout', '5s', true);
   RETURN;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
 END LOOP;
 RAISE EXCEPTION 'online migration step did not get its lock: queue_bindings_org_id_idx: drop an interrupted build' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- queue_bindings_org_id_idx: build concurrently (waits only for older transactions)
SET lock_timeout = '10s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS queue_bindings_org_id_idx ON queue_bindings (org_id);
SET lock_timeout = '500ms';
RESET lock_timeout;

-- +goose Down
-- Rollback is an operator action on a drained schema, not part of a release:
-- releases roll back by running the previous binaries against this schema.
SET lock_timeout = '5s';
-- +goose StatementBegin
-- Reservations cannot change counter ownership during a rollback.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM invocations WHERE quota_reserved AND async_quota_subject_id<>account_id) THEN
  RAISE EXCEPTION 'drain organization async reservations before rolling back queue ownership';
 END IF;
END $$;
DROP TRIGGER org_queue_bindings_guard ON orgs;
DROP FUNCTION guard_org_queue_bindings();
DROP TRIGGER IF EXISTS org_queue_reservation_adoption ON orgs;
DROP FUNCTION IF EXISTS adopt_queue_reservations_on_org_adoption();
DROP FUNCTION adopt_org_async_reservations(uuid);
DROP TRIGGER invocation_async_subject ON invocations;
DROP FUNCTION stamp_invocation_async_subject();
DROP FUNCTION invocation_async_subject(uuid,uuid);
ALTER TABLE invocations DROP COLUMN async_quota_subject_id;
ALTER TABLE orgs DROP COLUMN IF EXISTS async_quota_pooled_at;
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
RESET lock_timeout;
