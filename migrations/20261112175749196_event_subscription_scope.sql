-- filename: 20261112175749196_event_subscription_scope.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000: an event subscription belongs to one project
-- environment and receives only that environment's events. The empty scope is
-- the app's default (production) scope, which every existing subscription
-- keeps. A named scope carries the environment it was created in, so the
-- environment's deletion removes it.
ALTER TABLE event_subscriptions ADD COLUMN IF NOT EXISTS deployment_scope text NOT NULL DEFAULT '';
ALTER TABLE event_subscriptions ADD COLUMN IF NOT EXISTS environment_id uuid;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_subscriptions'::regclass AND conname='event_subscription_scope_shape') THEN
    ALTER TABLE event_subscriptions ADD CONSTRAINT event_subscription_scope_shape
      CHECK ((deployment_scope='' AND environment_id IS NULL) OR
        (deployment_scope ~ '^[a-z][a-z0-9-]{0,62}$' AND environment_id IS NOT NULL)) NOT VALID;
  END IF;
END $$;
-- The per-environment identity and the environment index are built
-- concurrently by 20261112175755118_event_subscription_scope_indexes; the
-- app-wide identity is dropped by the contract migration
-- 20261112175759744_event_subscription_drop_unscoped_identity.

-- A scoped subscription resolves its environment from the app's project when
-- it is written; its environment never changes afterwards.
CREATE OR REPLACE FUNCTION guard_event_subscription_environment_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE environment uuid;
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.environment_id IS DISTINCT FROM OLD.environment_id OR NEW.deployment_scope IS DISTINCT FROM OLD.deployment_scope
      OR (OLD.deployment_scope<>'' AND NEW.app_id IS DISTINCT FROM OLD.app_id) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='event_subscription_scope_identity',
        MESSAGE='event subscription environment identity is immutable';
    END IF;
  ELSIF NEW.deployment_scope<>'' THEN
    SELECT e.id INTO environment FROM apps a JOIN project_environments e ON e.project_id=a.project_id
      AND e.account_id=a.account_id AND e.slug=NEW.deployment_scope
      WHERE a.id=NEW.app_id AND a.status<>'deleted'
      FOR SHARE OF a,e;
    IF NOT FOUND OR (NEW.environment_id IS NOT NULL AND NEW.environment_id IS DISTINCT FROM environment) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='event_subscription_environment_membership',
        MESSAGE='an environment event subscription requires a project environment of its app';
    END IF;
    NEW.environment_id=environment;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS guard_event_subscription_environment_scope ON event_subscriptions;
CREATE TRIGGER guard_event_subscription_environment_scope BEFORE INSERT OR UPDATE OF deployment_scope, environment_id, app_id ON event_subscriptions
  FOR EACH ROW EXECUTE FUNCTION guard_event_subscription_environment_scope();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS guard_event_subscription_environment_scope ON event_subscriptions;
DROP FUNCTION IF EXISTS guard_event_subscription_environment_scope();
DELETE FROM event_subscriptions WHERE deployment_scope<>'';
ALTER TABLE event_subscriptions DROP CONSTRAINT IF EXISTS event_subscription_scope_shape;
ALTER TABLE event_subscriptions DROP COLUMN IF EXISTS environment_id;
ALTER TABLE event_subscriptions DROP COLUMN IF EXISTS deployment_scope;
-- +goose StatementEnd
