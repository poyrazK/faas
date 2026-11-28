-- filename: 20261127193058100_stored_workflow_environment.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000 amendment 2026-11-22: a stored workflow belongs to one
-- environment of its app's project. NULL is production, which every existing
-- workflow keeps. The environment's deletion removes its workflows.
ALTER TABLE stored_workflows ADD COLUMN IF NOT EXISTS environment_id uuid;
ALTER TABLE stored_workflow_revisions ADD COLUMN IF NOT EXISTS environment_id uuid;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='stored_workflows'::regclass AND conname='stored_workflows_environment_fkey') THEN
    ALTER TABLE stored_workflows ADD CONSTRAINT stored_workflows_environment_fkey
      FOREIGN KEY (environment_id) REFERENCES project_environments(id) ON DELETE CASCADE NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='stored_workflow_revisions'::regclass AND conname='stored_workflow_revisions_environment_fkey') THEN
    ALTER TABLE stored_workflow_revisions ADD CONSTRAINT stored_workflow_revisions_environment_fkey
      FOREIGN KEY (environment_id) REFERENCES project_environments(id) ON DELETE CASCADE NOT VALID;
  END IF;
  -- A stage runs its workflows on its own deployment for manual starts and
  -- schedules only; any other stage workflow (a clone copies every one)
  -- stays disabled.
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='stored_workflows'::regclass AND conname='stored_workflows_environment_enabled_chk') THEN
    ALTER TABLE stored_workflows ADD CONSTRAINT stored_workflows_environment_enabled_chk
      CHECK (environment_id IS NULL OR NOT enabled OR (published IS NOT NULL
        AND COALESCE(published->'trigger'->>'type', 'manual') IN ('manual', 'schedule'))) NOT VALID;
  END IF;
END $$;

-- A workflow's environment is one of its app's project environments and
-- never changes.
CREATE OR REPLACE FUNCTION guard_stored_workflow_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.environment_id IS DISTINCT FROM OLD.environment_id THEN
    RAISE EXCEPTION 'a stored workflow keeps its environment' USING ERRCODE = '23514';
  END IF;
  IF NEW.environment_id IS NOT NULL AND NOT EXISTS (
      SELECT 1 FROM apps a JOIN project_environments e ON e.project_id = a.project_id AND e.account_id = a.account_id
      WHERE a.id = NEW.app_id AND e.id = NEW.environment_id) THEN
    RAISE EXCEPTION 'a stored workflow environment belongs to its app''s project' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS stored_workflow_environment_guard ON stored_workflows;
CREATE TRIGGER stored_workflow_environment_guard BEFORE INSERT OR UPDATE OF environment_id, app_id ON stored_workflows
  FOR EACH ROW EXECUTE FUNCTION guard_stored_workflow_environment();

-- A revision takes its workflow's environment, whichever release writes it.
CREATE OR REPLACE FUNCTION stored_workflow_revision_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  SELECT w.environment_id INTO NEW.environment_id FROM stored_workflows w
  WHERE w.app_id = NEW.app_id AND w.name = NEW.name;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS stored_workflow_revision_environment ON stored_workflow_revisions;
CREATE TRIGGER stored_workflow_revision_environment BEFORE INSERT OR UPDATE OF environment_id, app_id, name ON stored_workflow_revisions
  FOR EACH ROW EXECUTE FUNCTION stored_workflow_revision_environment();

-- Production's runnable workflows (schedules, events, webhooks, manual
-- starts) never include a stage's, on this release or N-1.
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb) RETURNS jsonb
    LANGUAGE plpgsql STABLE
    AS $fn$
BEGIN
    RETURN (
        SELECT coalesce(jsonb_agg(CASE WHEN definition->'trigger'->>'type' IN ('schedule','event') AND EXISTS(SELECT 1 FROM stored_workflow_failure_guards g WHERE g.app_id=target_app AND g.name=definition->>'name' AND g.paused_at IS NOT NULL) THEN jsonb_set(definition,'{trigger,enabled}','false'::jsonb,true) ELSE definition END ORDER BY definition->>'name'),'[]'::jsonb)
        FROM (
            SELECT definition FROM jsonb_array_elements(CASE WHEN jsonb_typeof(manifest)='array' THEN manifest ELSE '[]'::jsonb END) definition
            WHERE NOT EXISTS(SELECT 1 FROM stored_workflows w WHERE w.app_id=target_app
              AND w.name=definition->>'name' AND w.published IS NOT NULL AND w.environment_id IS NULL)
            UNION ALL
            SELECT CASE WHEN published->'trigger'->>'type' IN ('schedule','event')
             THEN jsonb_set(published, '{trigger,enabled}', to_jsonb(enabled), true) ELSE published END
            FROM stored_workflows WHERE app_id=target_app AND published IS NOT NULL AND environment_id IS NULL
        ) effective);
END;
$fn$;
-- stored_workflows_environment_idx and the per-environment name index are
-- built concurrently by 20261127193058300_stored_workflow_environment_index.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb) RETURNS jsonb
    LANGUAGE plpgsql STABLE
    AS $fn$
BEGIN
    RETURN (
        SELECT coalesce(jsonb_agg(CASE WHEN definition->'trigger'->>'type' IN ('schedule','event') AND EXISTS(SELECT 1 FROM stored_workflow_failure_guards g WHERE g.app_id=target_app AND g.name=definition->>'name' AND g.paused_at IS NOT NULL) THEN jsonb_set(definition,'{trigger,enabled}','false'::jsonb,true) ELSE definition END ORDER BY definition->>'name'),'[]'::jsonb)
        FROM (
            SELECT definition FROM jsonb_array_elements(CASE WHEN jsonb_typeof(manifest)='array' THEN manifest ELSE '[]'::jsonb END) definition
            WHERE NOT EXISTS(SELECT 1 FROM stored_workflows w WHERE w.app_id=target_app
              AND w.name=definition->>'name' AND w.published IS NOT NULL)
            UNION ALL
            SELECT CASE WHEN published->'trigger'->>'type' IN ('schedule','event')
             THEN jsonb_set(published, '{trigger,enabled}', to_jsonb(enabled), true) ELSE published END
            FROM stored_workflows WHERE app_id=target_app AND published IS NOT NULL
        ) effective);
END;
$fn$;
DROP TRIGGER IF EXISTS stored_workflow_revision_environment ON stored_workflow_revisions;
DROP FUNCTION IF EXISTS stored_workflow_revision_environment();
DROP TRIGGER IF EXISTS stored_workflow_environment_guard ON stored_workflows;
DROP FUNCTION IF EXISTS guard_stored_workflow_environment();
DELETE FROM stored_workflow_revisions WHERE environment_id IS NOT NULL;
DELETE FROM stored_workflows WHERE environment_id IS NOT NULL;
ALTER TABLE stored_workflows DROP CONSTRAINT IF EXISTS stored_workflows_environment_enabled_chk;
ALTER TABLE stored_workflows DROP CONSTRAINT IF EXISTS stored_workflows_environment_fkey;
ALTER TABLE stored_workflow_revisions DROP CONSTRAINT IF EXISTS stored_workflow_revisions_environment_fkey;
ALTER TABLE stored_workflow_revisions DROP COLUMN IF EXISTS environment_id;
ALTER TABLE stored_workflows DROP COLUMN IF EXISTS environment_id;
-- +goose StatementEnd
