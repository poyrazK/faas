-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_binding_release_policies (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 mode text NOT NULL CHECK(mode IN ('off','enforce')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 max_age_seconds bigint NOT NULL CHECK(max_age_seconds BETWEEN 1 AND 86400),
 require_application_ack boolean NOT NULL DEFAULT false,
 reason text NOT NULL DEFAULT '' CHECK(octet_length(reason)<=256 AND reason !~ '[[:cntrl:]]'),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(app_id,scope)
);
CREATE TABLE IF NOT EXISTS app_binding_release_policy_history (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 policy jsonb NOT NULL CHECK(jsonb_typeof(policy)='object'),
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(app_id,scope,revision)
);
CREATE OR REPLACE FUNCTION capture_binding_release_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.app_id<>OLD.app_id OR NEW.scope<>OLD.scope OR NEW.revision<>OLD.revision+1) THEN
  RAISE EXCEPTION 'release policy revision changed' USING ERRCODE='23514',CONSTRAINT='binding_release_policy_revision';
 END IF;
 IF NEW.mode='off' AND btrim(NEW.reason)='' THEN
  RAISE EXCEPTION 'disabling enforcement requires a reason' USING ERRCODE='23514',CONSTRAINT='binding_release_policy_reason';
 END IF;
 PERFORM bump_binding_promotion_revision(NEW.app_id);
 INSERT INTO app_binding_release_policy_history(app_id,scope,revision,policy)
 VALUES(NEW.app_id,NEW.scope,NEW.revision,to_jsonb(NEW));
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER binding_release_policy_changed AFTER INSERT OR UPDATE ON app_binding_release_policies
 FOR EACH ROW EXECUTE FUNCTION capture_binding_release_policy();

-- APID supplies internally evaluated fences, never client request fields.
-- Compare evidence under the same revision lock that catalog changes take.
CREATE OR REPLACE FUNCTION authorize_binding_release_traffic(fences jsonb) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE f jsonb; observed text; d deployments%ROWTYPE; p app_binding_release_policies%ROWTYPE;
BEGIN
 FOR f IN SELECT value FROM jsonb_array_elements(fences) LOOP
  SELECT r.epoch::text||':'||r.revision::text INTO observed FROM app_binding_promotion_revisions r
  JOIN apps a ON a.id=r.app_id WHERE r.app_id=(f->>'app_id')::uuid AND a.account_id=(f->>'account_id')::uuid AND a.status<>'deleted'
  FOR UPDATE OF r;
  IF observed IS NULL OR observed<>f->>'revision' THEN
   RAISE EXCEPTION 'binding facts changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  SELECT * INTO d FROM deployments WHERE id=(f->>'deployment_id')::uuid AND app_id=(f->>'app_id')::uuid;
  IF d.id IS NULL OR d.status<>'live' OR d.scope<>f->>'scope' OR coalesce(d.rootfs_key,'')='' OR coalesce(d.image_digest,'')='' THEN
   RAISE EXCEPTION 'binding deployment changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  IF NULLIF(f->>'valid_until','')::timestamptz <= clock_timestamp() THEN
   RAISE EXCEPTION 'binding evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
  SELECT * INTO p FROM app_binding_release_policies WHERE app_id=d.app_id AND scope=d.scope;
  IF p.mode='enforce' AND NOT coalesce((f->>'policy_revision')::bigint=p.revision AND (f->>'max_age_seconds')::numeric>0 AND (f->>'max_age_seconds')::numeric<=p.max_age_seconds AND NOT (f->>'allow_unsupported')::boolean AND (NOT p.require_application_ack OR (f->>'require_application_ack')::boolean),false) THEN
   RAISE EXCEPTION 'binding release policy changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 PERFORM set_config('gregale.binding_release_fences',fences::text,true);
 RETURN true;
END $$;

CREATE OR REPLACE FUNCTION enforce_binding_release_traffic() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_percent integer:=0; p app_binding_release_policies%ROWTYPE; f jsonb; granted boolean:=false;
BEGIN
 IF TG_OP='UPDATE' AND OLD.status='live' AND OLD.app_id=NEW.app_id AND OLD.scope=NEW.scope THEN prior_percent:=OLD.traffic_percent; END IF;
 -- Admission with positive intended traffic is also gated, so unguarded
 -- automatic activation cannot fail only after a build has run.
 IF NEW.traffic_percent<=prior_percent OR NEW.traffic_percent=0 OR (TG_OP='UPDATE' AND NEW.status<>'live') THEN RETURN NEW; END IF;
 -- Even a missing policy must serialize with concurrent enabling.
 PERFORM 1 FROM app_binding_promotion_revisions WHERE app_id=NEW.app_id FOR UPDATE;
 SELECT * INTO p FROM app_binding_release_policies WHERE app_id=NEW.app_id AND scope=NEW.scope;
 IF p.mode IS DISTINCT FROM 'enforce' THEN RETURN NEW; END IF;
 FOR f IN SELECT value FROM jsonb_array_elements(coalesce(NULLIF(current_setting('gregale.binding_release_fences',true),''),'[]')::jsonb) LOOP
  IF f->>'deployment_id'=NEW.id::text AND f->>'app_id'=NEW.app_id::text AND f->>'scope'=NEW.scope
    AND (f->>'policy_revision')::bigint=p.revision
    AND (NULLIF(f->>'valid_until','') IS NULL OR (f->>'valid_until')::timestamptz>clock_timestamp()) THEN granted:=true; EXIT; END IF;
 END LOOP;
 IF NOT granted THEN
  RAISE EXCEPTION 'binding release requires fresh checked evidence' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER binding_release_traffic BEFORE INSERT OR UPDATE OF traffic_percent,status,scope,app_id ON deployments
 FOR EACH ROW EXECUTE FUNCTION enforce_binding_release_traffic();

-- Switching a project graph can route to zero-weight members without changing
-- deployments. Until graph-wide binding fences exist, fail closed as well.
CREATE OR REPLACE FUNCTION enforce_binding_release_graph() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid;
BEGIN
 IF (TG_OP='UPDATE' AND NEW.active=OLD.active) OR (TG_OP='INSERT' AND NOT NEW.active) THEN RETURN NEW; END IF;
 FOR app IN SELECT id FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' ORDER BY id LOOP
  PERFORM 1 FROM app_binding_promotion_revisions WHERE app_id=app FOR UPDATE;
  IF EXISTS(SELECT 1 FROM app_binding_release_policies WHERE app_id=app AND scope=NEW.environment_slug AND mode='enforce') THEN
   RAISE EXCEPTION 'project release switching requires a binding release gate' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER binding_release_graph BEFORE INSERT OR UPDATE OF active ON project_release_sets
 FOR EACH ROW EXECUTE FUNCTION enforce_binding_release_graph();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER binding_release_graph ON project_release_sets;
DROP FUNCTION enforce_binding_release_graph();
DROP TRIGGER binding_release_traffic ON deployments;
DROP FUNCTION enforce_binding_release_traffic();
DROP FUNCTION authorize_binding_release_traffic(jsonb);
DROP TRIGGER binding_release_policy_changed ON app_binding_release_policies;
DROP FUNCTION capture_binding_release_policy();
DROP TABLE app_binding_release_policy_history;
DROP TABLE app_binding_release_policies;
-- +goose StatementEnd
