-- filename: 20261003214107845_environment_secret_reference_suppression.sql
-- ADR-425: durable absence overrides legacy deployment and stage-all delivery.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_environment_secret_ref_suppressions (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z][a-z0-9-]{0,62}$'),
 key text NOT NULL CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND octet_length(key)<=128),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (app_id, environment_id, key)
);
CREATE OR REPLACE FUNCTION environment_scoped_secret_suppressions(target_app uuid,target_scope text) RETURNS text[]
LANGUAGE sql STABLE AS $$
 SELECT coalesce(array_agg(r.key ORDER BY r.key),ARRAY[]::text[])
 FROM app_environment_secret_ref_suppressions r JOIN project_environments e ON e.id=r.environment_id
 JOIN apps a ON a.id=r.app_id AND a.account_id=r.account_id AND a.project_id=r.project_id
 WHERE r.app_id=target_app AND e.slug=target_scope AND e.account_id=r.account_id AND e.project_id=r.project_id;
$$;
CREATE OR REPLACE FUNCTION guard_environment_secret_ref_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_value record; src environment_git_sources%ROWTYPE;
 resource_name text; controller boolean; stamp timestamptz;
BEGIN
 IF TG_OP='DELETE' THEN row_value:=OLD; ELSE row_value:=NEW; END IF;
 -- Parent cascades have already removed their catalog/app identity. Ordinary
 -- row deletion still passes through ownership enforcement below.
 IF TG_OP='DELETE' AND NOT EXISTS (SELECT 1 FROM apps a JOIN project_environments e ON e.id=row_value.environment_id
  WHERE a.id=row_value.app_id AND a.account_id=row_value.account_id AND a.project_id=row_value.project_id
   AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=row_value.scope) THEN
  -- Removing catalog intent also invalidates caches captured under its UUID.
  -- The surviving app can retain deployments/sealed provider credentials.
  IF EXISTS(SELECT 1 FROM apps WHERE id=row_value.app_id FOR UPDATE) THEN
   stamp:=clock_timestamp();
   INSERT INTO app_runtime_config_scope_changes(app_id,scope,changed_at) VALUES(row_value.app_id,row_value.scope,stamp)
    ON CONFLICT(app_id,scope) DO UPDATE SET changed_at=greatest(app_runtime_config_scope_changes.changed_at,excluded.changed_at);
   UPDATE snapshots p SET stale=true FROM deployments d WHERE p.deployment_id=d.id AND d.app_id=row_value.app_id
    AND d.scope=row_value.scope AND NOT p.stale;
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.project_id IS DISTINCT FROM OLD.project_id
  OR NEW.environment_id IS DISTINCT FROM OLD.environment_id OR NEW.app_id IS DISTINCT FROM OLD.app_id
  OR NEW.scope IS DISTINCT FROM OLD.scope OR NEW.key IS DISTINCT FROM OLD.key) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_identity',MESSAGE='secret reference identity is immutable';
 END IF;
 SELECT s.* INTO src FROM environment_git_sources s
 WHERE s.environment_id=row_value.environment_id AND s.account_id=row_value.account_id FOR UPDATE OF s;
 IF TG_OP<>'DELETE' AND NOT EXISTS (SELECT 1 FROM apps a JOIN project_environments e ON e.id=row_value.environment_id
  WHERE a.id=row_value.app_id AND a.account_id=row_value.account_id AND a.project_id=row_value.project_id
   AND a.status<>'deleted' AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=row_value.scope
  FOR SHARE OF a,e) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_scope',MESSAGE='secret reference requires its account-owned project environment';
 END IF;
 IF src.id IS NOT NULL THEN
  SELECT logical_name INTO resource_name FROM environment_gitops_resources WHERE source_id=src.id AND app_id=row_value.app_id;
  controller:=EXISTS (SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id
   AND j.desired_generation=src.generation AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp()
   AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
  IF src.mode='enforce' AND NOT controller AND EXISTS (
   SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=resource_name
    AND f.field_path='secret_refs/'||row_value.key AND NOT EXISTS (SELECT 1 FROM environment_management_overrides o
     WHERE o.environment_id=f.environment_id AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_gitops_field_owned',MESSAGE='setting is managed by the environment Git source';
  END IF;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END IF;
 -- Publication and every ordinary writer share the app lock and scoped stamp.
 IF EXISTS(SELECT 1 FROM apps WHERE id=row_value.app_id FOR UPDATE) THEN
  stamp:=clock_timestamp();
  INSERT INTO app_runtime_config_scope_changes(app_id,scope,changed_at) VALUES(row_value.app_id,row_value.scope,stamp)
   ON CONFLICT(app_id,scope) DO UPDATE SET changed_at=greatest(app_runtime_config_scope_changes.changed_at,excluded.changed_at);
  UPDATE snapshots p SET stale=true FROM deployments d WHERE p.deployment_id=d.id AND d.app_id=row_value.app_id
   AND d.scope=row_value.scope AND NOT p.stale;
 END IF;
 -- Both kinds of intent share the same app lock. Direct SQL cannot retain
 -- a positive reference and a suppression for the same destination.
 IF TG_OP<>'DELETE' THEN
  IF (TG_TABLE_NAME='app_environment_secret_refs' AND EXISTS(SELECT 1 FROM app_environment_secret_ref_suppressions
    WHERE app_id=row_value.app_id AND environment_id=row_value.environment_id AND key=row_value.key))
   OR (TG_TABLE_NAME='app_environment_secret_ref_suppressions' AND EXISTS(SELECT 1 FROM app_environment_secret_refs
    WHERE app_id=row_value.app_id AND environment_id=row_value.environment_id AND key=row_value.key)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_exclusive',MESSAGE='reference and suppression are mutually exclusive';
  END IF;
  -- Mirrored by api.EnvironmentSecretReferenceSuppressionsMaxPerApp.
  IF TG_TABLE_NAME='app_environment_secret_ref_suppressions' AND TG_OP='INSERT'
   AND NOT EXISTS(SELECT 1 FROM app_environment_secret_ref_suppressions WHERE app_id=row_value.app_id AND environment_id=row_value.environment_id AND key=row_value.key)
   AND (SELECT count(*) FROM app_environment_secret_ref_suppressions WHERE app_id=row_value.app_id)>=1024 THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='environment_secret_ref_suppression_quota',MESSAGE='secret reference suppression limit reached';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE NEW.updated_at:=stamp; RETURN NEW; END IF;
END;
$$;
DROP TRIGGER IF EXISTS environment_secret_ref_intent ON app_environment_secret_refs;
CREATE TRIGGER environment_secret_ref_intent BEFORE INSERT OR UPDATE OR DELETE ON app_environment_secret_refs
 FOR EACH ROW EXECUTE FUNCTION guard_environment_secret_ref_intent();

DROP TRIGGER IF EXISTS environment_secret_ref_intent ON app_environment_secret_ref_suppressions;
CREATE TRIGGER environment_secret_ref_intent BEFORE INSERT OR UPDATE OR DELETE ON app_environment_secret_ref_suppressions
 FOR EACH ROW EXECUTE FUNCTION guard_environment_secret_ref_intent();

CREATE OR REPLACE FUNCTION environment_runtime_inputs_fresh(target_app uuid,target_scope text,boundary timestamptz,
 observed_variables jsonb,observed_secrets jsonb,observed_all_secrets boolean,observed_secret_refs jsonb)
RETURNS boolean LANGUAGE sql STABLE AS $$
 WITH managed AS (SELECT environment_scoped_secret_refs(target_app,target_scope) AS refs),
 suppressed AS (SELECT environment_scoped_secret_suppressions(target_app,target_scope) AS keys),
 baseline AS (SELECT coalesce(jsonb_object_agg(s.key,'secret:'||s.key),'{}'::jsonb) AS refs
  FROM app_secrets s WHERE s.app_id=target_app AND s.scope=target_scope)
 SELECT environment_runtime_base_inputs_fresh(target_app,target_scope,boundary,observed_variables,observed_secrets,false)
  AND managed.refs <@ observed_secret_refs
  AND NOT observed_secret_refs ?| suppressed.keys
  AND (cardinality(suppressed.keys)=0 OR observed_secrets=coalesce((SELECT jsonb_object_agg(s.scope||'/'||s.key,s.delivery_version)
   FROM app_secrets s WHERE s.app_id=target_app AND s.scope=target_scope AND EXISTS(
    SELECT 1 FROM jsonb_each_text(observed_secret_refs) r WHERE r.value='secret:'||s.key)),'{}'::jsonb))
  AND NOT EXISTS(SELECT 1 FROM jsonb_each(managed.refs) r WHERE observed_variables ? r.key)
  AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secret_refs) r WHERE
   NOT EXISTS (SELECT 1 FROM app_secrets s WHERE s.app_id=target_app AND s.scope=target_scope
    AND r.value='secret:'||s.key AND observed_secrets->>(target_scope||'/'||s.key)=s.delivery_version::text))
  AND (NOT observed_all_secrets OR
   CASE WHEN observed_secret_refs='{}'::jsonb AND managed.refs='{}'::jsonb AND cardinality(suppressed.keys)=0
    THEN environment_runtime_base_inputs_fresh(target_app,target_scope,boundary,observed_variables,observed_secrets,true)
    ELSE observed_secret_refs=((baseline.refs - suppressed.keys)||managed.refs) END)
 FROM managed,baseline,suppressed;
$$;
CREATE OR REPLACE FUNCTION environment_runtime_receipt_required(target_app uuid,target_scope text) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT environment_scoped_secret_refs(target_app,target_scope)<>'{}'::jsonb
  OR cardinality(environment_scoped_secret_suppressions(target_app,target_scope))>0 OR EXISTS (
  SELECT 1 FROM environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
  JOIN environment_gitops_resources r ON r.source_id=s.id WHERE r.app_id=target_app AND e.slug=target_scope
  AND (EXISTS (SELECT 1 FROM environment_managed_fields f WHERE f.source_id=s.id AND f.resource=r.logical_name
   AND (f.field_path LIKE 'variables/%' OR f.field_path LIKE 'secret_refs/%'))
   OR EXISTS (SELECT 1 FROM environment_gitops_runtime_effects x WHERE x.source_id=s.id AND x.app_id=r.app_id AND x.completed_at IS NULL)));
$$;

CREATE OR REPLACE VIEW environment_gitops_runtime_targets AS
WITH targets AS (
    SELECT s.id AS source_id, s.account_id, r.app_id, r.logical_name AS resource, e.slug AS environment_slug
    FROM environment_git_sources s
    JOIN project_environments e ON e.id = s.environment_id
    JOIN environment_gitops_resources r ON r.source_id = s.id
    JOIN apps a ON a.id = r.app_id AND a.account_id = s.account_id AND a.project_id = s.project_id
    WHERE cardinality(environment_scoped_secret_suppressions(r.app_id,e.slug))>0
       OR EXISTS (SELECT 1 FROM environment_managed_fields f WHERE f.source_id = s.id
        AND f.resource = r.logical_name AND (f.field_path LIKE 'variables/%' OR f.field_path LIKE 'secret_refs/%'))
       OR EXISTS (SELECT 1 FROM environment_gitops_runtime_effects x WHERE x.source_id = s.id
        AND x.app_id = r.app_id AND x.completed_at IS NULL)
), boundaries AS (
    SELECT t.*, greatest(
        coalesce((SELECT c.changed_at FROM app_runtime_config_changes c WHERE c.app_id = t.app_id), 'epoch'::timestamptz),
        coalesce((SELECT max(c.changed_at) FROM app_runtime_config_scope_changes c
            WHERE c.app_id = t.app_id AND c.scope IN ('default', t.environment_slug)), 'epoch'::timestamptz),
        coalesce((SELECT max(v.updated_at) FROM app_envs v JOIN environment_managed_fields f
            ON f.source_id = t.source_id AND f.resource = t.resource AND f.field_path = 'variables/' || v.key
            WHERE v.app_id = t.app_id AND v.scope = t.environment_slug), 'epoch'::timestamptz),
        coalesce((SELECT max(x.required_at) FROM environment_gitops_runtime_effects x
            WHERE x.source_id = t.source_id AND x.app_id = t.app_id AND x.completed_at IS NULL), 'epoch'::timestamptz)
    ) AS required_at FROM targets t
)
SELECT b.*,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting', 'running', 'warm', 'draining')
        AND NOT EXISTS (SELECT 1 FROM instance_runtime_config_receipts r WHERE r.instance_id = i.id AND r.wake_id = i.wake_id
            AND r.scope = b.environment_slug AND r.boundary_at >= b.required_at
            AND environment_runtime_inputs_fresh(b.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets, r.secret_refs))) AS stale_residents,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting', 'migrating')) AS starting_residents,
    (SELECT count(*) FROM snapshots p JOIN deployments d ON d.id = p.deployment_id
        WHERE d.app_id = b.app_id AND d.scope = b.environment_slug
        AND NOT p.stale AND NOT p.delete_pending
        AND NOT EXISTS (SELECT 1 FROM snapshot_runtime_config_receipts r WHERE r.snapshot_id = p.id
            AND r.scope = b.environment_slug AND r.boundary_at >= b.required_at
            AND environment_runtime_inputs_fresh(b.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets, r.secret_refs))) AS stale_snapshots
FROM boundaries b;

-- A deployment promotion or legacy mapping edit cannot race reviewed adoption.
-- Existing writers that hold app/deployment locks first may deadlock; PostgreSQL
-- aborts one whole transaction and the controller retries its durable work.
CREATE OR REPLACE FUNCTION guard_environment_secret_reference_baseline() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_app uuid; new_app uuid; old_scope text; new_scope text; src environment_git_sources%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' AND OLD.status='live' THEN old_app:=OLD.app_id; old_scope:=OLD.scope; END IF;
 IF TG_OP<>'DELETE' AND NEW.status='live' THEN new_app:=NEW.app_id; new_scope:=NEW.scope; END IF;
 IF old_app IS NULL AND new_app IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 FOR src IN SELECT s.* FROM environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
  JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
  WHERE (a.id=old_app AND e.slug=old_scope) OR (a.id=new_app AND e.slug=new_scope)
  ORDER BY s.id FOR UPDATE OF s LOOP
  PERFORM 1 FROM apps a WHERE (a.id=old_app OR a.id=new_app) AND a.project_id=src.project_id ORDER BY a.id FOR UPDATE;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
DROP TRIGGER IF EXISTS environment_secret_reference_baseline ON deployments;
CREATE TRIGGER environment_secret_reference_baseline BEFORE INSERT OR DELETE OR UPDATE OF status,scope,app_id,override_env_secrets ON deployments
 FOR EACH ROW EXECUTE FUNCTION guard_environment_secret_reference_baseline();
-- +goose StatementEnd
-- +goose Down
-- Preserve negative intent and receipts through rollback.
SELECT 1;
