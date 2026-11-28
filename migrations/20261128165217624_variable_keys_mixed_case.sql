-- filename: 20261128165217624_variable_keys_mixed_case.sql
-- migration-compat: expand
-- ADR-20261128195037: sealed secrets take the same mixed-case key shape as
-- plain variables (^[A-Za-z][A-Za-z0-9_]*$, <= 128 bytes), so DOTNET_gcServer
-- or next_public_url can be sealed. Widened: app and project environment
-- secret keys, guard-retained secret keys, secret reference source names and
-- revocation keys. Also fixes two places that silently refused mixed-case
-- variables the API accepts: project environment configuration
-- (project_environment_config_env dropped lowercase keys, contrary to
-- docs/env.md) and Git-reviewed workload variables (the intent trigger
-- refused what environmentsync validates with api.ValidateEnvKey).
-- Each wider CHECK is added NOT VALID under a new name and validated, then
-- the old one dropped; re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $widen1$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secrets'::regclass AND conname = 'app_secrets_key_shape_v2') THEN
    ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_key_shape_v2
      CHECK (key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$widen1$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $widen2$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_secrets'::regclass AND conname = 'project_environment_secrets_key_shape_v2') THEN
    ALTER TABLE project_environment_secrets ADD CONSTRAINT project_environment_secrets_key_shape_v2
      CHECK (key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$widen2$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $widen3$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_config_guard_secrets'::regclass AND conname = 'app_config_guard_secrets_config_key_v2_check') THEN
    ALTER TABLE app_config_guard_secrets ADD CONSTRAINT app_config_guard_secrets_config_key_v2_check
      CHECK (config_key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(config_key) <= 128) NOT VALID;
  END IF;
END
$widen3$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $widen4$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'app_environment_secret_refs_secret_name_v2_check') THEN
    ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_secret_name_v2_check
      CHECK (secret_name ~ '^[A-Za-z][A-Za-z0-9_]*$' AND octet_length(secret_name) <= 128) NOT VALID;
  END IF;
END
$widen4$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $widen5$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secret_revocations'::regclass AND conname = 'app_secret_revocations_key_shape_v2') THEN
    ALTER TABLE app_secret_revocations ADD CONSTRAINT app_secret_revocations_key_shape_v2
      CHECK (key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$widen5$;
-- +goose StatementEnd
ALTER TABLE app_secrets VALIDATE CONSTRAINT app_secrets_key_shape_v2;
ALTER TABLE project_environment_secrets VALIDATE CONSTRAINT project_environment_secrets_key_shape_v2;
ALTER TABLE app_config_guard_secrets VALIDATE CONSTRAINT app_config_guard_secrets_config_key_v2_check;
ALTER TABLE app_environment_secret_refs VALIDATE CONSTRAINT app_environment_secret_refs_secret_name_v2_check;
ALTER TABLE app_secret_revocations VALIDATE CONSTRAINT app_secret_revocations_key_shape_v2;
ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_key_shape;
ALTER TABLE project_environment_secrets DROP CONSTRAINT IF EXISTS project_environment_secrets_key_shape;
ALTER TABLE app_config_guard_secrets DROP CONSTRAINT IF EXISTS app_config_guard_secrets_config_key_check;
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_secret_name_check;
ALTER TABLE app_secret_revocations DROP CONSTRAINT IF EXISTS app_secret_revocations_key_shape;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.project_environment_config_env(config jsonb) RETURNS jsonb
    LANGUAGE sql IMMUTABLE
    AS $_$
    SELECT coalesce(jsonb_object_agg(e.key, e.value), '{}'::jsonb)
      FROM jsonb_each(CASE WHEN jsonb_typeof(config) = 'object' THEN config ELSE '{}'::jsonb END) e
     WHERE e.key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(e.key) <= 128
       AND jsonb_typeof(e.value) IN ('string', 'number', 'boolean');
$_$;
-- +goose StatementEnd

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
  v.key !~ '^[A-Za-z][A-Za-z0-9_]*$' OR length(v.key)>128 OR jsonb_typeof(v.value)<>'string' OR
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

-- +goose Down
SET lock_timeout = '5s';
-- Mixed-case rows written since stay; the restored uppercase CHECKs are
-- NOT VALID so the rollback does not delete customer configuration, and
-- they refuse new mixed-case writes again.

-- +goose StatementBegin
DO $restore1$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secrets'::regclass AND conname = 'app_secrets_key_shape') THEN
    ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_key_shape
      CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$restore1$;
-- +goose StatementEnd
ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_key_shape_v2;
-- +goose StatementBegin
DO $restore2$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_secrets'::regclass AND conname = 'project_environment_secrets_key_shape') THEN
    ALTER TABLE project_environment_secrets ADD CONSTRAINT project_environment_secrets_key_shape
      CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$restore2$;
-- +goose StatementEnd
ALTER TABLE project_environment_secrets DROP CONSTRAINT IF EXISTS project_environment_secrets_key_shape_v2;
-- +goose StatementBegin
DO $restore3$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_config_guard_secrets'::regclass AND conname = 'app_config_guard_secrets_config_key_check') THEN
    ALTER TABLE app_config_guard_secrets ADD CONSTRAINT app_config_guard_secrets_config_key_check
      CHECK (config_key ~ '^[A-Z][A-Z0-9_]*$' AND length(config_key) <= 128) NOT VALID;
  END IF;
END
$restore3$;
-- +goose StatementEnd
ALTER TABLE app_config_guard_secrets DROP CONSTRAINT IF EXISTS app_config_guard_secrets_config_key_v2_check;
-- +goose StatementBegin
DO $restore4$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'app_environment_secret_refs_secret_name_check') THEN
    ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_secret_name_check
      CHECK (secret_name ~ '^[A-Z][A-Z0-9_]*$' AND octet_length(secret_name) <= 128) NOT VALID;
  END IF;
END
$restore4$;
-- +goose StatementEnd
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_secret_name_v2_check;
-- +goose StatementBegin
DO $restore5$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_secret_revocations'::regclass AND conname = 'app_secret_revocations_key_shape') THEN
    ALTER TABLE app_secret_revocations ADD CONSTRAINT app_secret_revocations_key_shape
      CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$restore5$;
-- +goose StatementEnd
ALTER TABLE app_secret_revocations DROP CONSTRAINT IF EXISTS app_secret_revocations_key_shape_v2;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.project_environment_config_env(config jsonb) RETURNS jsonb
    LANGUAGE sql IMMUTABLE
    AS $_$
    SELECT coalesce(jsonb_object_agg(e.key, e.value), '{}'::jsonb)
      FROM jsonb_each(CASE WHEN jsonb_typeof(config) = 'object' THEN config ELSE '{}'::jsonb END) e
     WHERE e.key ~ '^[A-Z][A-Z0-9_]*$' AND length(e.key) <= 128
       AND jsonb_typeof(e.value) IN ('string', 'number', 'boolean');
$_$;
-- +goose StatementEnd

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
