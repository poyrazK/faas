-- filename: 20261128105922300_stored_workflow_revision_environment.sql
-- migration-compat: expand
-- ADR-20261104120000 amendment 2026-11-28: the next release lets a stage
-- reuse a production workflow name, so a revision can no longer find its
-- workflow by (app_id, name) alone. This release writes the revision's
-- environment itself; the trigger keeps it and fills it only for a writer
-- that leaves it out (N-1), preferring production's workflow of that name.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION stored_workflow_revision_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.environment_id IS NULL THEN
    SELECT w.environment_id INTO NEW.environment_id FROM stored_workflows w
    WHERE w.app_id = NEW.app_id AND w.name = NEW.name
    ORDER BY (w.environment_id IS NULL) DESC
    LIMIT 1;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION stored_workflow_revision_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  SELECT w.environment_id INTO NEW.environment_id FROM stored_workflows w
  WHERE w.app_id = NEW.app_id AND w.name = NEW.name;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
