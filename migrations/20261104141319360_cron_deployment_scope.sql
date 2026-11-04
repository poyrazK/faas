-- filename: 20261104141319360_cron_deployment_scope.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000: a cron belongs to one project environment. The empty
-- scope is the app's default (production) scope, which every existing cron
-- keeps. A named scope carries the environment it was created in, as a
-- scoped queue binding does, so the environment's deletion removes it.
ALTER TABLE crons ADD COLUMN IF NOT EXISTS deployment_scope text NOT NULL DEFAULT '';
ALTER TABLE crons ADD COLUMN IF NOT EXISTS environment_id uuid;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='crons'::regclass AND conname='cron_scope_shape') THEN
    ALTER TABLE crons ADD CONSTRAINT cron_scope_shape
      CHECK ((deployment_scope='' AND environment_id IS NULL) OR
        (deployment_scope ~ '^[a-z][a-z0-9-]{0,62}$' AND environment_id IS NOT NULL)) NOT VALID;
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS crons_environment_idx ON crons (environment_id) WHERE environment_id IS NOT NULL;
-- A stage may hold the same schedule, path and command as production: the
-- identity is unique within one environment of an app.
CREATE UNIQUE INDEX IF NOT EXISTS crons_app_scope_schedule_path_command_unique
  ON crons (app_id, deployment_scope, schedule, path, command);
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_app_schedule_path_command_unique;

-- A scoped cron resolves its environment from the app's project when it is
-- written; its environment never changes afterwards.
CREATE OR REPLACE FUNCTION guard_cron_environment_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE environment uuid;
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.environment_id IS DISTINCT FROM OLD.environment_id OR NEW.deployment_scope IS DISTINCT FROM OLD.deployment_scope
      OR (OLD.deployment_scope<>'' AND NEW.app_id IS DISTINCT FROM OLD.app_id) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='cron_scope_identity',
        MESSAGE='cron environment identity is immutable';
    END IF;
  ELSIF NEW.deployment_scope<>'' THEN
    SELECT e.id INTO environment FROM apps a JOIN project_environments e ON e.project_id=a.project_id
      AND e.account_id=a.account_id AND e.slug=NEW.deployment_scope
      WHERE a.id=NEW.app_id AND a.status<>'deleted'
      FOR SHARE OF a,e;
    IF NOT FOUND OR (NEW.environment_id IS NOT NULL AND NEW.environment_id IS DISTINCT FROM environment) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='cron_environment_membership',
        MESSAGE='an environment cron requires a project environment of its app';
    END IF;
    NEW.environment_id=environment;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS guard_cron_environment_scope ON crons;
CREATE TRIGGER guard_cron_environment_scope BEFORE INSERT OR UPDATE OF deployment_scope, environment_id, app_id ON crons
  FOR EACH ROW EXECUTE FUNCTION guard_cron_environment_scope();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS guard_cron_environment_scope ON crons;
DROP FUNCTION IF EXISTS guard_cron_environment_scope();
DROP INDEX IF EXISTS crons_environment_idx;
DELETE FROM crons WHERE deployment_scope<>'';
DROP INDEX IF EXISTS crons_app_scope_schedule_path_command_unique;
ALTER TABLE crons ADD CONSTRAINT crons_app_schedule_path_command_unique UNIQUE (app_id, schedule, path, command);
ALTER TABLE crons DROP CONSTRAINT IF EXISTS cron_scope_shape;
ALTER TABLE crons DROP COLUMN IF EXISTS environment_id;
ALTER TABLE crons DROP COLUMN IF EXISTS deployment_scope;
-- +goose StatementEnd
