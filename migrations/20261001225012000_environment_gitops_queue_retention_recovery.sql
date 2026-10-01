-- filename: 20261001225012000_environment_gitops_queue_retention_recovery.sql
-- ADR-425: reviewed retirement retains work; recovery names the original UUID.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_gitops_queue_recovery_declared(target_source uuid,target_binding uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_git_sources s
  JOIN environment_desired_revisions v ON v.id=s.approved_revision_id AND v.source_id=s.id
  JOIN environment_gitops_resources r ON r.source_id=s.id
  JOIN queue_bindings b ON b.id=target_binding AND b.app_id=r.app_id AND b.account_id=s.account_id AND b.environment_id=s.environment_id
  JOIN project_environments e ON e.id=s.environment_id AND e.account_id=s.account_id AND e.project_id=s.project_id
  WHERE s.id=target_source AND r.logical_name LIKE 'workload/%'
   AND v.definition->'workloads'->substr(r.logical_name,10)->'queue_recoveries'->>b.name=b.id::text
   AND v.definition->'workloads'->substr(r.logical_name,10)->'queue_bindings' ? b.name);
$$;

CREATE OR REPLACE FUNCTION environment_gitops_queue_recovery_authorized(target_binding uuid)
RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE src environment_git_sources%ROWTYPE;
BEGIN
 -- Legacy SQL writers may acquire the binding first. Serialize authority on
 -- the source too; a deadlock aborts the whole transaction rather than granting
 -- a stale approved generation permission to release retained work.
 SELECT s.* INTO src FROM environment_git_sources s JOIN queue_bindings b ON b.environment_id=s.environment_id
  JOIN apps a ON a.id=b.app_id AND a.account_id=s.account_id AND a.project_id=s.project_id
  WHERE b.id=target_binding AND b.account_id=s.account_id FOR UPDATE OF s;
 IF src.id IS NULL OR src.mode<>'enforce' OR src.suspended THEN RETURN false; END IF;
 RETURN environment_gitops_queue_recovery_declared(src.id,target_binding) AND EXISTS(
  SELECT 1 FROM environment_gitops_jobs j
   JOIN environment_gitops_queue_bindings q ON q.source_id=j.source_id AND q.binding_id=target_binding
   JOIN environment_managed_fields f ON f.source_id=q.source_id AND f.resource=q.resource AND f.field_path=q.field_path
  WHERE j.source_id=src.id AND j.desired_generation=src.generation AND j.claimed_generation=src.generation
   AND j.lease_until>clock_timestamp() AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=src.environment_id
    AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp()));
END;
$$;

CREATE OR REPLACE FUNCTION guard_queue_binding_retirement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.retired_at IS NOT NULL AND EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='queue_binding_retirement_identity',MESSAGE='retired queue identity requires explicit recovery';
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.retired_at IS NOT NULL AND NEW.retired_at IS NULL
  AND (to_jsonb(NEW)-'retired_at'-'updated_at')=(to_jsonb(OLD)-'retired_at'-'updated_at')
  AND environment_gitops_queue_recovery_authorized(OLD.id) THEN
  RETURN NEW;
 END IF;
 IF OLD.retired_at IS NOT NULL AND
  (NEW.retired_at IS DISTINCT FROM OLD.retired_at OR NEW.id IS DISTINCT FROM OLD.id
   OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.app_id IS DISTINCT FROM OLD.app_id
   OR NEW.name IS DISTINCT FROM OLD.name OR NEW.queue_name IS DISTINCT FROM OLD.queue_name
   OR NEW.mode IS DISTINCT FROM OLD.mode OR NEW.workload_class IS DISTINCT FROM OLD.workload_class
   OR NEW.enabled IS DISTINCT FROM OLD.enabled OR NEW.max_concurrency IS DISTINCT FROM OLD.max_concurrency
   OR NEW.retry_policy IS DISTINCT FROM OLD.retry_policy) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='queue_binding_retirement_identity',MESSAGE='retired queue binding requires explicit recovery';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_environment_gitops_queue_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.source_id IS DISTINCT FROM OLD.source_id OR NEW.resource IS DISTINCT FROM OLD.resource
  OR NEW.field_path IS DISTINCT FROM OLD.field_path OR (OLD.binding_id IS NOT NULL AND NEW.binding_id IS NOT NULL AND NEW.binding_id IS DISTINCT FROM OLD.binding_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_queue_identity',MESSAGE='Git queue identity requires reviewed recovery';
 END IF;
 IF NEW.binding_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM queue_bindings b JOIN environment_git_sources s ON s.id=NEW.source_id
   JOIN environment_gitops_resources r ON r.source_id=s.id AND r.logical_name=NEW.resource
  WHERE b.id=NEW.binding_id AND b.app_id=r.app_id AND b.account_id=s.account_id
   AND b.environment_id=s.environment_id AND NEW.field_path='queue_bindings/'||b.name
   AND (b.retired_at IS NULL OR environment_gitops_queue_recovery_declared(s.id,b.id)) FOR SHARE OF b) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_queue_identity',MESSAGE='Git queue ownership requires its original scoped binding and reviewed recovery';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- Preserve retained work, management identity and recovery authority.
SELECT 1;
