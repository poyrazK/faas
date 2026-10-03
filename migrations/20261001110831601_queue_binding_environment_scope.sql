-- filename: 20261001110831601_queue_binding_environment_scope.sql

-- +goose Up
ALTER TABLE queue_bindings ADD COLUMN IF NOT EXISTS deployment_scope text NOT NULL DEFAULT '';
ALTER TABLE queue_bindings ADD COLUMN IF NOT EXISTS environment_id uuid;
ALTER TABLE triggers ADD COLUMN IF NOT EXISTS queue_binding_scope text NOT NULL DEFAULT '';
ALTER TABLE triggers ADD COLUMN IF NOT EXISTS queue_binding_environment_id uuid;
-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='queue_bindings'::regclass
    AND conname='queue_binding_scope_shape') THEN
    ALTER TABLE queue_bindings ADD CONSTRAINT queue_binding_scope_shape
      CHECK ((deployment_scope='' AND environment_id IS NULL) OR
        (deployment_scope ~ '^[a-z][a-z0-9-]{0,62}$' AND environment_id IS NOT NULL));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='triggers'::regclass
    AND conname='queue_consumer_scope_shape') THEN
    ALTER TABLE triggers ADD CONSTRAINT queue_consumer_scope_shape
      CHECK ((queue_binding_scope='' AND queue_binding_environment_id IS NULL) OR (queue_binding_id IS NOT NULL
        AND queue_binding_scope ~ '^[a-z][a-z0-9-]{0,62}$' AND queue_binding_environment_id IS NOT NULL));
  END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_queue_binding_environment_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE environment uuid;
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.environment_id IS DISTINCT FROM OLD.environment_id OR NEW.deployment_scope IS DISTINCT FROM OLD.deployment_scope OR
      (OLD.deployment_scope<>'' AND (NEW.id IS DISTINCT FROM OLD.id
        OR NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.account_id IS DISTINCT FROM OLD.account_id)) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_scope_identity',
        MESSAGE='queue binding environment identity is immutable';
    END IF;
  ELSIF NEW.deployment_scope<>'' THEN
    SELECT e.id INTO environment FROM apps a JOIN project_environments e ON e.project_id=a.project_id
      AND e.account_id=a.account_id AND e.slug=NEW.deployment_scope
      WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id AND a.status<>'deleted'
      FOR SHARE OF a,e;
    IF NOT FOUND OR (NEW.environment_id IS NOT NULL AND NEW.environment_id IS DISTINCT FROM environment) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_environment_membership',
        MESSAGE='scoped queue binding requires an account-owned project environment';
    END IF;
    NEW.environment_id=environment;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_queue_consumer_environment_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE scope text; environment uuid;
BEGIN
  IF TG_OP='UPDATE' AND (NEW.queue_binding_scope IS DISTINCT FROM OLD.queue_binding_scope
    OR NEW.queue_binding_environment_id IS DISTINCT FROM OLD.queue_binding_environment_id) THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_consumer_scope_identity',
      MESSAGE='queue consumer environment identity is immutable';
  END IF;
  IF NEW.queue_binding_id IS NULL THEN
    IF NEW.queue_binding_scope<>'' OR NEW.queue_binding_environment_id IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_consumer_scope_identity',
        MESSAGE='scoped consumer requires an immutable binding';
    END IF;
  ELSE
    SELECT b.deployment_scope,b.environment_id INTO scope,environment FROM queue_bindings b
      WHERE b.id=NEW.queue_binding_id AND b.app_id=NEW.app_id AND b.account_id=NEW.account_id FOR SHARE;
    IF NOT FOUND THEN RETURN NEW; END IF; -- The existing deferred tenant FK owns this rejection.
    IF (TG_OP='UPDATE' AND (NEW.queue_binding_scope IS DISTINCT FROM scope OR NEW.queue_binding_environment_id IS DISTINCT FROM environment))
      OR (TG_OP='INSERT' AND ((NEW.queue_binding_scope<>'' AND NEW.queue_binding_scope IS DISTINCT FROM scope)
        OR (NEW.queue_binding_environment_id IS NOT NULL AND NEW.queue_binding_environment_id IS DISTINCT FROM environment))) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_consumer_scope_identity',
        MESSAGE='queue consumer must retain its parent environment';
    END IF;
    NEW.queue_binding_scope=scope;
    NEW.queue_binding_environment_id=environment;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_invocation_queue_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.queue_binding_id IS DISTINCT FROM OLD.queue_binding_id OR
      (OLD.queue_binding_id IS NOT NULL AND (NEW.app_id IS DISTINCT FROM OLD.app_id
        OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.queue_name IS DISTINCT FROM OLD.queue_name)) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_identity',
        MESSAGE='accepted queue binding identity is immutable';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.queue_binding_id IS NOT NULL AND NEW.source<>'queue' THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_source',
      MESSAGE='binding identity requires a queue invocation';
  END IF;
  IF NEW.source='queue' THEN
    IF NEW.queue_binding_id IS NULL AND NEW.queue_name<>'' THEN
      SELECT b.id INTO NEW.queue_binding_id FROM queue_bindings b
        WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND b.queue_name=NEW.queue_name
          AND (b.deployment_scope='' OR (b.deployment_scope=NEW.deployment_scope AND EXISTS
            (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
              WHERE a.id=NEW.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope)))
        ORDER BY (b.deployment_scope<>'') DESC LIMIT 1;
    ELSIF NEW.queue_binding_id IS NULL AND NEW.work_policy_name IS NULL THEN
      SELECT b.id INTO NEW.queue_binding_id FROM queue_bindings b JOIN triggers t ON t.queue_binding_id=b.id
        WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND b.enabled
          AND (b.deployment_scope='' OR (b.deployment_scope=NEW.deployment_scope AND EXISTS
            (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
              WHERE a.id=NEW.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope)))
          AND b.mode='push' AND b.retired_at IS NULL AND t.enabled AND t.kind='queue' AND t.source='queue'
          AND NOT EXISTS (SELECT 1 FROM triggers other WHERE other.app_id=NEW.app_id
            AND other.kind='queue' AND other.source='queue' AND other.enabled AND other.id<>t.id
            AND (other.queue_binding_scope='' OR (other.queue_binding_scope=NEW.deployment_scope AND EXISTS
              (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
                WHERE a.id=NEW.app_id AND e.id=other.queue_binding_environment_id AND e.slug=other.queue_binding_scope))));
    END IF;
    IF NEW.queue_binding_id IS NOT NULL THEN
      PERFORM 1 FROM queue_bindings b WHERE b.id=NEW.queue_binding_id
        AND b.app_id=NEW.app_id AND b.account_id=NEW.account_id
        AND (b.deployment_scope='' OR (b.deployment_scope=NEW.deployment_scope AND EXISTS
            (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
              WHERE a.id=NEW.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope))) FOR SHARE;
      IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_tenant',
          MESSAGE='queue binding must belong to the admitted app, account and environment';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_retired_queue_invocation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE binding_retired_at timestamptz; binding_scope text; binding_environment uuid;
BEGIN
  IF NEW.source='queue' AND (TG_OP='INSERT' OR
    (NEW.state='dispatching' AND OLD.state IS DISTINCT FROM 'dispatching')) THEN
    SELECT b.retired_at,b.deployment_scope,b.environment_id INTO binding_retired_at,binding_scope,binding_environment FROM queue_bindings b
      WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND
        (b.id=NEW.queue_binding_id OR (NEW.queue_binding_id IS NULL
          AND b.deployment_scope='' AND b.queue_name=NEW.queue_name)) FOR SHARE;
    IF binding_scope<>'' AND NOT EXISTS (SELECT 1 FROM apps a JOIN project_environments e
      ON e.project_id=a.project_id AND e.account_id=a.account_id WHERE a.id=NEW.app_id
        AND e.id=binding_environment AND e.slug=binding_scope) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_environment_unavailable',
        MESSAGE='captured queue binding environment is unavailable';
    END IF;
    IF binding_retired_at IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_retired', MESSAGE='queue binding is retired';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

-- This independent guard remains installed while older unreleased migrations
-- replay their earlier admission/retirement function definitions.
CREATE OR REPLACE FUNCTION guard_scoped_queue_binding_work() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE scope text; environment uuid;
BEGIN
  IF NEW.source='queue' AND NEW.queue_binding_id IS NOT NULL AND (TG_OP='INSERT'
    OR (NEW.state='dispatching' AND OLD.state IS DISTINCT FROM 'dispatching')) THEN
    SELECT b.deployment_scope,b.environment_id INTO scope,environment FROM queue_bindings b
      WHERE b.id=NEW.queue_binding_id AND b.app_id=NEW.app_id AND b.account_id=NEW.account_id FOR SHARE;
    IF scope<>'' THEN
      IF scope IS DISTINCT FROM NEW.deployment_scope THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_tenant',
          MESSAGE='queue binding must retain the admitted environment';
      END IF;
      PERFORM 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
        WHERE a.id=NEW.app_id AND e.id=environment AND e.slug=scope FOR SHARE OF a,e;
      IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_environment_unavailable',
          MESSAGE='captured queue binding environment is unavailable';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_scoped_queue_consumer_receipt() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM triggers t WHERE t.id=NEW.trigger_id AND t.queue_binding_scope<>'')
    AND NOT EXISTS (SELECT 1 FROM triggers t JOIN invocations i ON i.id::text=NEW.item_identifier
      AND i.app_id=t.app_id AND i.account_id=t.account_id AND i.source='queue'
      AND i.queue_binding_id=t.queue_binding_id AND i.deployment_scope=t.queue_binding_scope
      WHERE t.id=NEW.trigger_id) THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_consumer_receipt_scope',
      MESSAGE='scoped consumer receipt requires work captured for its binding';
  END IF;
  IF NEW.state='claimed' AND (TG_OP='INSERT' OR OLD.state IS DISTINCT FROM 'claimed'
    OR NEW.claim_generation IS DISTINCT FROM OLD.claim_generation) AND EXISTS (
      SELECT 1 FROM triggers t WHERE t.id=NEW.trigger_id AND t.queue_binding_scope<>'') THEN
    PERFORM 1 FROM triggers t JOIN apps a ON a.id=t.app_id JOIN project_environments e
        ON e.project_id=a.project_id AND e.account_id=a.account_id AND e.id=t.queue_binding_environment_id
        AND e.slug=t.queue_binding_scope WHERE t.id=NEW.trigger_id FOR SHARE OF a,e;
    IF NOT FOUND THEN RETURN NULL; END IF;
  END IF;
  RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION guard_held_queue_consumer_receipt() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE can_claim boolean;
BEGIN
  IF NEW.state='claimed' AND (OLD.state IS DISTINCT FROM 'claimed'
    OR NEW.claim_generation IS DISTINCT FROM OLD.claim_generation) THEN
    SELECT b.enabled AND b.mode='push' AND b.retired_at IS NULL AND (b.deployment_scope='' OR EXISTS (
      SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
        WHERE a.id=b.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope)) INTO can_claim
      FROM queue_bindings b JOIN triggers t ON t.queue_binding_id=b.id WHERE t.id=NEW.trigger_id FOR SHARE OF b;
    IF can_claim IS FALSE THEN RETURN NULL; END IF;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Separate legacy NULL-identity indexes preserve uniqueness on the supported
-- PostgreSQL 13+ hosts as well as the named environment identities.
DROP INDEX IF EXISTS queue_bindings_app_name_uniq;
CREATE UNIQUE INDEX queue_bindings_app_name_uniq ON queue_bindings(app_id,environment_id,name);
CREATE UNIQUE INDEX IF NOT EXISTS queue_bindings_app_legacy_name_unique ON queue_bindings(app_id,name) WHERE environment_id IS NULL;
DROP INDEX IF EXISTS queue_bindings_app_queue_name_uniq;
CREATE UNIQUE INDEX queue_bindings_app_queue_name_uniq ON queue_bindings(app_id,environment_id,queue_name);
CREATE UNIQUE INDEX IF NOT EXISTS queue_bindings_app_legacy_queue_name_unique ON queue_bindings(app_id,queue_name) WHERE environment_id IS NULL;
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_app_id_slug_key;
DROP INDEX IF EXISTS triggers_app_scope_slug_unique;
CREATE UNIQUE INDEX triggers_app_scope_slug_unique ON triggers(app_id,queue_binding_environment_id,slug);
CREATE UNIQUE INDEX IF NOT EXISTS triggers_app_legacy_slug_unique ON triggers(app_id,slug) WHERE queue_binding_environment_id IS NULL;
DROP INDEX IF EXISTS triggers_one_enabled_queue_source;
CREATE UNIQUE INDEX triggers_one_enabled_queue_source ON triggers(app_id,source)
  WHERE kind='queue' AND enabled AND source IS NOT NULL AND queue_binding_scope='';
DROP TRIGGER IF EXISTS queue_binding_environment_scope_guard ON queue_bindings;
CREATE TRIGGER queue_binding_environment_scope_guard BEFORE INSERT OR UPDATE ON queue_bindings
  FOR EACH ROW EXECUTE FUNCTION guard_queue_binding_environment_scope();
DROP TRIGGER IF EXISTS queue_consumer_environment_scope_guard ON triggers;
CREATE TRIGGER queue_consumer_environment_scope_guard BEFORE INSERT OR UPDATE ON triggers
  FOR EACH ROW EXECUTE FUNCTION guard_queue_consumer_environment_scope();
DROP TRIGGER IF EXISTS queue_consumer_receipt_scope_guard ON trigger_records;
CREATE TRIGGER queue_consumer_receipt_scope_guard BEFORE INSERT OR UPDATE OF trigger_id,item_identifier,state,claim_generation ON trigger_records
  FOR EACH ROW EXECUTE FUNCTION guard_scoped_queue_consumer_receipt();

DROP TRIGGER IF EXISTS invocation_scoped_queue_environment_guard ON invocations;
CREATE TRIGGER invocation_scoped_queue_environment_guard BEFORE INSERT OR UPDATE OF state ON invocations
  FOR EACH ROW EXECUTE FUNCTION guard_scoped_queue_binding_work();

-- +goose Down
-- Keep immutable scope, accepted work and consumer receipts on rollback.
SELECT 1;
