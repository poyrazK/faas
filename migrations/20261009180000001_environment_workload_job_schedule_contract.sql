-- +goose Up
-- GitOps owns the reviewed recurring schedule and non-secret variables for a
-- job workload. The following migrations bind it to a durable Gregale Job.
ALTER TABLE app_environment_workload_intents
 ADD COLUMN IF NOT EXISTS schedule jsonb;
ALTER TABLE app_environment_workload_intents
 ADD COLUMN IF NOT EXISTS variables jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE app_environment_workload_intents
 DROP CONSTRAINT IF EXISTS app_environment_workload_intents_variables_check;
ALTER TABLE app_environment_workload_intents
 ADD CONSTRAINT app_environment_workload_intents_variables_check CHECK (jsonb_typeof(variables)='object');
ALTER TABLE app_environment_workload_intents
 DROP CONSTRAINT IF EXISTS environment_workload_job_schedule_shape;
ALTER TABLE app_environment_workload_intents
 ADD CONSTRAINT environment_workload_job_schedule_shape CHECK (
  schedule IS NULL OR (
   jsonb_typeof(schedule)='object' AND
   jsonb_typeof(schedule->'cron')='string' AND btrim(schedule->>'cron')<>'' AND
   jsonb_typeof(schedule->'timezone')='string' AND btrim(schedule->>'timezone')<>''
  )
 );

-- Treat schedule as an owned intent path in the same lease and generation
-- fence as source, runtime and service bindings. A direct SQL update cannot
-- change an enforced schedule outside the active GitOps executor.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
$$;
-- +goose StatementEnd


-- +goose Down
-- Keep the downgrade safe when the new intent is in use.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM app_environment_workload_intents WHERE schedule IS NOT NULL OR variables<>'{}'::jsonb) THEN
        RAISE EXCEPTION 'scheduled workload inputs exist; remove schedules and variables before downgrading';
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_intent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
 SELECT s.* INTO src FROM active_environment_git_sources s WHERE s.account_id=(row_value->>'account_id')::uuid
  AND s.environment_id=(row_value->>'environment_id')::uuid FOR UPDATE OF s;
 -- ON CONFLICT fires the INSERT trigger before its UPDATE trigger. Compare
 -- against the original scoped row so an unchanged owned field is not an edit.
 IF TG_OP='INSERT' THEN
  SELECT to_jsonb(w) INTO prior FROM app_environment_workload_intents w
   WHERE w.app_id=NEW.app_id AND w.environment_id=NEW.environment_id FOR UPDATE OF w;
  prior:=coalesce(prior,'{}');
 ELSE prior:=to_jsonb(OLD); END IF;
 IF TG_OP='DELETE' THEN row_value:=row_value||jsonb_build_object('source',NULL,'source_revision',NULL,'runtime','{}'::jsonb,'service_bindings','{}'::jsonb); END IF;
 SELECT array_agg('runtime/'||k) INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'runtime','{}')) UNION SELECT key FROM jsonb_each(row_value->'runtime')
 ) keys WHERE (prior->'runtime'->k) IS DISTINCT FROM (row_value->'runtime'->k);
 IF coalesce(prior->'source','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source']; END IF;
 IF coalesce(prior->'source_revision','null'::jsonb) IS DISTINCT FROM coalesce(row_value->'source_revision','null'::jsonb) THEN paths:=coalesce(paths,'{}')||ARRAY['source_revision','source']; END IF;
 SELECT coalesce(paths,'{}')||coalesce(array_agg('service_bindings/'||k),'{}') INTO paths FROM (
  SELECT key k FROM jsonb_each(coalesce(prior->'service_bindings','{}')) UNION SELECT key FROM jsonb_each(row_value->'service_bindings')
 ) keys WHERE (prior->'service_bindings'->k) IS DISTINCT FROM (row_value->'service_bindings'->k);
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.service_bindings) b WHERE
  b.key !~ '^[a-z0-9][a-z0-9-]*$' OR jsonb_typeof(b.value)<>'object' OR
  NOT b.value ?& ARRAY['workload','env_key','target_app_id'] OR b.value->>'env_key' !~ '^[A-Za-z_][A-Za-z0-9_]*$' OR
  NOT EXISTS(SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id
   WHERE r.source_id=src.id AND r.logical_name='workload/'||(b.value->>'workload') AND a.id=(b.value->>'target_app_id')::uuid
   AND a.id<>NEW.app_id AND a.account_id=NEW.account_id AND a.project_id=src.project_id AND a.status<>'deleted')) THEN
  RAISE EXCEPTION 'scoped service binding requires its original environment target' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND ((SELECT count(*) FROM jsonb_object_keys(NEW.service_bindings))>100 OR EXISTS(
  SELECT 1 FROM jsonb_each(NEW.service_bindings) GROUP BY value->>'env_key' HAVING count(*)>1)) THEN
  RAISE EXCEPTION 'scoped service binding count or environment keys are invalid' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND NEW.source->>'kind'='function' AND NOT EXISTS(SELECT 1 FROM apps a
  WHERE a.id=NEW.app_id AND a.type='function' AND a.runtime=NEW.source->>'runtime') THEN
  RAISE EXCEPTION 'function source requires its original supported runner' USING ERRCODE='23514';
 END IF;
 IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM jsonb_each(NEW.service_bindings) b
  JOIN project_environments e ON e.id=NEW.environment_id WHERE
   EXISTS(SELECT 1 FROM app_envs v WHERE v.app_id=NEW.app_id AND v.scope=e.slug AND v.key=b.value->>'env_key') OR
   EXISTS(SELECT 1 FROM app_environment_secret_refs r WHERE r.app_id=NEW.app_id AND r.environment_id=e.id AND r.key=b.value->>'env_key')) THEN
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
$$;
-- +goose StatementEnd

ALTER TABLE app_environment_workload_intents
    DROP CONSTRAINT IF EXISTS environment_workload_job_schedule_shape;
ALTER TABLE app_environment_workload_intents DROP COLUMN IF EXISTS schedule;
ALTER TABLE app_environment_workload_intents DROP CONSTRAINT IF EXISTS app_environment_workload_intents_variables_check;
ALTER TABLE app_environment_workload_intents DROP COLUMN IF EXISTS variables;
