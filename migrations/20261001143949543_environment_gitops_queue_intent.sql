-- filename: 20261001143949543_environment_gitops_queue_intent.sql

-- +goose Up
-- ADR-423: logical queue ownership pins durable binding identity; it does not
-- adopt shared queues, release a retirement hold, or remove delivery history.
CREATE TABLE IF NOT EXISTS environment_gitops_queue_bindings (
 source_id uuid NOT NULL,
 resource text NOT NULL,
 field_path text NOT NULL CHECK (field_path ~ '^queue_bindings/[a-z][a-z0-9-]{0,62}$'),
 binding_id uuid UNIQUE REFERENCES queue_bindings(id) ON DELETE SET NULL,
 PRIMARY KEY (source_id,resource,field_path),
 FOREIGN KEY (source_id,resource) REFERENCES environment_gitops_resources(source_id,logical_name) ON DELETE CASCADE
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_gitops_queue_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.source_id IS DISTINCT FROM OLD.source_id OR NEW.resource IS DISTINCT FROM OLD.resource
   OR NEW.field_path IS DISTINCT FROM OLD.field_path OR (OLD.binding_id IS NOT NULL AND NEW.binding_id IS NOT NULL AND NEW.binding_id IS DISTINCT FROM OLD.binding_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_queue_identity',MESSAGE='Git queue identity requires reviewed recovery';
 END IF;
 IF NEW.binding_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM queue_bindings b JOIN environment_git_sources s ON s.id=NEW.source_id
   JOIN environment_gitops_resources r ON r.source_id=s.id AND r.logical_name=NEW.resource
  WHERE b.id=NEW.binding_id AND b.app_id=r.app_id AND b.account_id=s.account_id
   AND b.environment_id=s.environment_id AND b.retired_at IS NULL AND NEW.field_path='queue_bindings/'||b.name
  FOR SHARE OF b) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_queue_identity',MESSAGE='Git queue ownership requires its original active scoped binding';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS environment_gitops_queue_identity ON environment_gitops_queue_bindings;
CREATE TRIGGER environment_gitops_queue_identity BEFORE INSERT OR UPDATE ON environment_gitops_queue_bindings
 FOR EACH ROW EXECUTE FUNCTION guard_environment_gitops_queue_identity();

CREATE OR REPLACE FUNCTION guard_environment_gitops_queue_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 binding queue_bindings%ROWTYPE;
 src environment_git_sources%ROWTYPE;
 resource_name text;
 paths text[];
 controller boolean;
BEGIN
 IF TG_TABLE_NAME='queue_bindings' THEN
  IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at'-'created_at')=(to_jsonb(OLD)-'updated_at'-'created_at') THEN RETURN NEW; END IF;
  IF TG_OP='DELETE' THEN binding:=OLD; ELSE binding:=NEW; END IF;
  paths:=ARRAY['queue_bindings/'||binding.name];
  IF TG_OP='UPDATE' THEN paths:=paths||ARRAY['queue_bindings/'||OLD.name]; END IF;
 ELSE
  IF TG_OP='UPDATE' AND OLD.queue_binding_id IS NOT NULL AND NEW.id IS DISTINCT FROM OLD.id THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='queue_consumer_binding_identity',MESSAGE='private consumer UUID is immutable';
  END IF;
  IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at'-'created_at')=(to_jsonb(OLD)-'updated_at'-'created_at') THEN RETURN NEW; END IF;
  IF TG_OP='DELETE' THEN
   SELECT b.* INTO binding FROM queue_bindings b WHERE b.id=OLD.queue_binding_id;
  ELSE
   SELECT b.* INTO binding FROM queue_bindings b WHERE b.id=NEW.queue_binding_id;
  END IF;
  paths:=ARRAY['queue_bindings/'||binding.name];
 END IF;
 IF binding.environment_id IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 SELECT s.* INTO src FROM environment_git_sources s JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
  WHERE s.environment_id=binding.environment_id AND s.account_id=binding.account_id AND a.id=binding.app_id FOR UPDATE OF s;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=binding.app_id;
  SELECT paths||coalesce(array_agg(q.field_path),'{}'::text[]) INTO paths FROM environment_gitops_queue_bindings q
   WHERE q.source_id=src.id AND q.binding_id=binding.id;
  controller:=EXISTS (SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id
   AND j.desired_generation=src.generation AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp()
   AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS (
   SELECT 1 FROM environment_managed_fields f WHERE f.environment_id=src.environment_id AND f.resource=resource_name
    AND f.field_path=ANY(paths) AND f.source_id=src.id AND NOT EXISTS (
     SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=f.environment_id AND o.resource=f.resource
      AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
    ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
-- Run after the existing scope trigger has captured the catalog UUID.
DROP TRIGGER IF EXISTS zz_environment_gitops_guard_queue_binding ON queue_bindings;
CREATE TRIGGER zz_environment_gitops_guard_queue_binding BEFORE INSERT OR UPDATE OR DELETE ON queue_bindings
 FOR EACH ROW EXECUTE FUNCTION guard_environment_gitops_queue_intent();
DROP TRIGGER IF EXISTS zz_environment_gitops_guard_queue_consumer ON triggers;
CREATE TRIGGER zz_environment_gitops_guard_queue_consumer BEFORE INSERT OR UPDATE OR DELETE ON triggers
 FOR EACH ROW EXECUTE FUNCTION guard_environment_gitops_queue_intent();
-- +goose StatementEnd

-- +goose Down
-- Preserve management intent, original binding identity and accepted work.
SELECT 1;
