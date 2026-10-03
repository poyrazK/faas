-- ADR-429: source/runtime intent has an original catalog identity, not app scope.
-- +goose Up
CREATE TABLE IF NOT EXISTS app_environment_workload_intents (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 source jsonb CHECK (source IS NULL OR jsonb_typeof(source)='object'),
 runtime jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(runtime)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(app_id,environment_id)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 row_value jsonb; prior jsonb; src environment_git_sources%ROWTYPE;
 resource_name text; paths text[]; controller boolean;
BEGIN
 IF TG_OP='DELETE' THEN row_value:=to_jsonb(OLD); ELSE row_value:=to_jsonb(NEW); END IF;
 -- Parent purges remove their original identity before cascading. Ordinary
 -- scoped-row deletion still requires ownership authority below.
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
 SELECT s.* INTO src FROM environment_git_sources s WHERE s.account_id=(row_value->>'account_id')::uuid
  AND s.environment_id=(row_value->>'environment_id')::uuid FOR UPDATE OF s;
 -- ON CONFLICT fires the INSERT trigger before its UPDATE trigger. Compare
 -- against the original scoped row so an unchanged owned field is not an edit.
 IF TG_OP='INSERT' THEN
  SELECT to_jsonb(w) INTO prior FROM app_environment_workload_intents w
   WHERE w.app_id=NEW.app_id AND w.environment_id=NEW.environment_id FOR UPDATE OF w;
  prior:=coalesce(prior,'{}');
 ELSE prior:=to_jsonb(OLD); END IF;
 IF TG_OP='DELETE' THEN row_value:=row_value||jsonb_build_object('source',NULL,'runtime','{}'::jsonb); END IF;
 SELECT array_agg('runtime/'||k) INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'runtime','{}')) UNION SELECT key FROM jsonb_each(row_value->'runtime')
 ) keys WHERE (prior->'runtime'->k) IS DISTINCT FROM (row_value->'runtime'->k);
 IF coalesce(prior->'source','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source']; END IF;
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
$$;
DROP TRIGGER IF EXISTS environment_workload_intent_guard ON app_environment_workload_intents;
CREATE TRIGGER environment_workload_intent_guard BEFORE INSERT OR UPDATE OR DELETE ON app_environment_workload_intents
 FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_intent();
-- +goose StatementEnd
-- +goose Down
-- Preserve adopted settings and management identity on rollback.
SELECT 1;
