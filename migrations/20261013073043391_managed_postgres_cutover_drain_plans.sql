-- filename: 20261013073043391_managed_postgres_cutover_drain_plans.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_cutover_drain_plans (
 id uuid PRIMARY KEY,
 cutover_id uuid NOT NULL,
 app_id uuid NOT NULL,
 owner_node_id uuid NOT NULL,
 owner_revision bigint NOT NULL CHECK (owner_revision > 0),
 fenced_at timestamptz NOT NULL,
 inventory_count integer NOT NULL CHECK (inventory_count >= 0),
 blocker_code text NOT NULL DEFAULT '' CHECK (blocker_code IN ('','inventory_empty','inventory_limit_exceeded')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (cutover_id,owner_node_id,owner_revision,fenced_at),
 CHECK (blocker_code <> '' OR inventory_count > 0)
);
CREATE TABLE IF NOT EXISTS managed_postgres_cutover_drain_plan_members (
 plan_id uuid NOT NULL REFERENCES managed_postgres_cutover_drain_plans(id),
 instance_id uuid NOT NULL,
 deployment_id uuid,
 node_id uuid NOT NULL,
 wake_id uuid NOT NULL,
 instance_kind text NOT NULL CHECK (instance_kind IN ('wake','build','job_task')),
 instance_mode text NOT NULL CHECK (instance_mode IN ('normal','mirror','job','worker','service')),
 observed_state text NOT NULL CHECK (observed_state IN ('pending','parked','waking','cold_booting','running','draining','snapshotting','migrating','warm','stopped','failed','evicting_account_deleting')),
 deployment_scope text NOT NULL,
 migrated_from_node_id uuid,
 qualification_live boolean NOT NULL,
 drain_id uuid REFERENCES managed_postgres_cutover_drains(id),
 blocker_code text NOT NULL CHECK (blocker_code IN ('','instance_identity_incomplete','unsupported_instance_kind','migration_lineage_unproven','qualification_cleanup_required','parked_instance_unproven','undispatched_instance_unproven','unsupported_instance_state')),
 PRIMARY KEY (plan_id,instance_id),
 CHECK ((drain_id IS NULL) = (blocker_code <> ''))
);
-- No app, cutover, deployment, node or instance FKs: retained evidence must
-- survive their deletion. The two evidence tables and native receipts remain
-- linked to each other and cannot silently lose a captured member.
CREATE INDEX IF NOT EXISTS managed_postgres_cutover_drain_plans_app_idx ON managed_postgres_cutover_drain_plans(app_id);
CREATE OR REPLACE FUNCTION guard_managed_postgres_drain_plan_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'cutover drain plans are immutable' USING ERRCODE='23514';
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_drain_plan_evidence_guard ON managed_postgres_cutover_drain_plans;
CREATE TRIGGER managed_postgres_drain_plan_evidence_guard BEFORE UPDATE OR DELETE ON managed_postgres_cutover_drain_plans
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_drain_plan_evidence();
DROP TRIGGER IF EXISTS managed_postgres_drain_plan_member_evidence_guard ON managed_postgres_cutover_drain_plan_members;
CREATE TRIGGER managed_postgres_drain_plan_member_evidence_guard BEFORE UPDATE OR DELETE ON managed_postgres_cutover_drain_plan_members
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_drain_plan_evidence();
DROP TRIGGER IF EXISTS managed_postgres_drain_plan_truncate_guard ON managed_postgres_cutover_drain_plans;
CREATE TRIGGER managed_postgres_drain_plan_truncate_guard BEFORE TRUNCATE ON managed_postgres_cutover_drain_plans
 FOR EACH STATEMENT EXECUTE FUNCTION guard_managed_postgres_drain_plan_evidence();
DROP TRIGGER IF EXISTS managed_postgres_drain_plan_member_truncate_guard ON managed_postgres_cutover_drain_plan_members;
CREATE TRIGGER managed_postgres_drain_plan_member_truncate_guard BEFORE TRUNCATE ON managed_postgres_cutover_drain_plan_members
 FOR EACH STATEMENT EXECUTE FUNCTION guard_managed_postgres_drain_plan_evidence();

CREATE OR REPLACE FUNCTION current_managed_postgres_drain_plan(app uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE authority apps%ROWTYPE; selected uuid;
BEGIN
 -- Lock even when no plan is visible yet. A parked/terminal INSERT that was
 -- admitted before reservation must wait, then observe the committed plan.
 SELECT * INTO authority FROM apps WHERE id=app FOR SHARE;
 IF authority.managed_postgres_admission_cutover_id IS NOT NULL
  AND current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION 'drain membership requires a current snapshot' USING ERRCODE='40001';
 END IF;
 SELECT id INTO selected FROM managed_postgres_cutover_drain_plans
 WHERE app_id=app AND cutover_id=authority.managed_postgres_admission_cutover_id
  AND fenced_at=authority.managed_postgres_admission_fenced_at
  AND owner_node_id=authority.node_id AND owner_revision=authority.scaling_policy_revision;
 RETURN selected;
END;
$$;
CREATE OR REPLACE FUNCTION guard_managed_postgres_drain_plan_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected uuid;
BEGIN
 IF TG_OP='INSERT' THEN
  selected := current_managed_postgres_drain_plan(NEW.app_id);
 ELSIF TG_OP='DELETE' THEN
  selected := current_managed_postgres_drain_plan(OLD.app_id);
  IF selected IS NULL THEN RETURN OLD; END IF;
  IF EXISTS (SELECT 1 FROM managed_postgres_cutover_drain_plan_members m
   JOIN managed_postgres_cutover_drain_plans p ON p.id=m.plan_id
   JOIN managed_postgres_cutover_drains d ON d.id=m.drain_id
   WHERE m.plan_id=selected AND m.instance_id=OLD.id AND d.state='retired'
    AND d.cutover_id=p.cutover_id AND d.app_id=p.app_id AND d.owner_node_id=p.owner_node_id
    AND d.owner_revision=p.owner_revision AND d.fenced_at=p.fenced_at
    AND d.instance_id=OLD.id AND d.deployment_id=OLD.deployment_id AND d.node_id=OLD.node_id AND d.wake_id=OLD.wake_id
    AND OLD.state='stopped') THEN RETURN OLD; END IF;
 ELSE
  -- Ordinary runtime counters and terminal transitions do not change catalog
  -- membership. Avoid adding an app lock to those hot paths.
  IF ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.kind,NEW.mode,NEW.migrated_from_node_id)
    IS NOT DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.kind,OLD.mode,OLD.migrated_from_node_id)
    AND (NEW.state <> 'migrating' OR NEW.state=OLD.state) THEN RETURN NEW; END IF;
  selected := current_managed_postgres_drain_plan(OLD.app_id);
  IF selected IS NULL AND NEW.app_id IS DISTINCT FROM OLD.app_id THEN
   selected := current_managed_postgres_drain_plan(NEW.app_id);
  END IF;
 END IF;
 IF selected IS NOT NULL THEN
  RAISE EXCEPTION 'cutover drain inventory is fenced' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_admission_fenced';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_drain_plan_inventory_guard ON instances;
CREATE TRIGGER managed_postgres_drain_plan_inventory_guard BEFORE INSERT OR UPDATE OR DELETE ON instances
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_drain_plan_inventory();
CREATE OR REPLACE FUNCTION guard_managed_postgres_drain_plan_inventory_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 -- TRUNCATE covers every app, including fenced apps created after a stale
 -- transaction snapshot. Reject stale isolation before consulting that snapshot.
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION 'drain inventory truncation requires a current snapshot' USING ERRCODE='40001';
 END IF;
 IF EXISTS (SELECT 1 FROM apps a JOIN managed_postgres_cutover_drain_plans p ON p.app_id=a.id
  AND p.cutover_id=a.managed_postgres_admission_cutover_id AND p.fenced_at=a.managed_postgres_admission_fenced_at
  AND p.owner_node_id=a.node_id AND p.owner_revision=a.scaling_policy_revision) THEN
  RAISE EXCEPTION 'cutover drain inventory is fenced' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_admission_fenced';
 END IF;
 RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_drain_plan_inventory_truncate_guard ON instances;
CREATE TRIGGER managed_postgres_drain_plan_inventory_truncate_guard BEFORE TRUNCATE ON instances
 FOR EACH STATEMENT EXECUTE FUNCTION guard_managed_postgres_drain_plan_inventory_truncate();

CREATE OR REPLACE FUNCTION lock_clone_deployment_cleanup_borrower_writers() RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE deployment_runtime_upgrade_acceptances,deployment_runtime_upgrade_baselines,deployment_runtime_upgrade_cutovers,environment_workload_serving_queue_acks,environment_workload_serving_routes,project_environment_clone_migration_adoptions,project_environment_clone_migration_resumes,project_environment_clone_wake_dispatches,route_probe_observations,runtime_upgrade_gateway_drains,runtime_upgrade_gateway_receipts,workflow_schedule_occurrences,app_issues,debug_regression_observations,deployment_audit,issue_events,issue_releases,issue_resolutions,log_events,mirror_invocation_results,org_activity,project_environment_clone_workloads,project_environment_promotion_workloads,request_audit_events,request_telemetry,upload_commit_outcomes,upload_sessions IN SHARE MODE;
 -- Retained PostgreSQL receipts and blocked plan members also borrow deployments.
 LOCK TABLE managed_postgres_cutover_drains,managed_postgres_cutover_drain_plan_members IN SHARE MODE;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_cutover_drain_plans) OR EXISTS (SELECT 1 FROM managed_postgres_cutover_drain_plan_members) THEN
  RAISE EXCEPTION 'drain plans must be preserved before rollback';
 END IF;
END $$;
DROP TRIGGER managed_postgres_drain_plan_inventory_guard ON instances;
DROP TRIGGER managed_postgres_drain_plan_inventory_truncate_guard ON instances;
DROP FUNCTION guard_managed_postgres_drain_plan_inventory();
DROP FUNCTION guard_managed_postgres_drain_plan_inventory_truncate();
DROP FUNCTION current_managed_postgres_drain_plan(uuid);
-- Restore the pre-plan writer inventory before removing its referenced table.
CREATE OR REPLACE FUNCTION lock_clone_deployment_cleanup_borrower_writers() RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE deployment_runtime_upgrade_acceptances,deployment_runtime_upgrade_baselines,deployment_runtime_upgrade_cutovers,environment_workload_serving_queue_acks,environment_workload_serving_routes,project_environment_clone_migration_adoptions,project_environment_clone_migration_resumes,project_environment_clone_wake_dispatches,route_probe_observations,runtime_upgrade_gateway_drains,runtime_upgrade_gateway_receipts,workflow_schedule_occurrences,app_issues,debug_regression_observations,deployment_audit,issue_events,issue_releases,issue_resolutions,log_events,mirror_invocation_results,org_activity,project_environment_clone_workloads,project_environment_promotion_workloads,request_audit_events,request_telemetry,upload_commit_outcomes,upload_sessions IN SHARE MODE;
END $$;
DROP TABLE managed_postgres_cutover_drain_plan_members;
DROP TABLE managed_postgres_cutover_drain_plans;
DROP FUNCTION guard_managed_postgres_drain_plan_evidence();
-- +goose StatementEnd
