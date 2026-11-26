-- filename: 20261125221809100_api_consumer_environment.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000 amendment 2026-11-22: an API consumer and its keys
-- belong to one environment of the app's project. NULL is production, which
-- every existing consumer and key keeps. The environment's deletion removes
-- its consumers and keys.
ALTER TABLE api_consumers ADD COLUMN IF NOT EXISTS environment_id uuid;
ALTER TABLE consumer_keys ADD COLUMN IF NOT EXISTS environment_id uuid;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='api_consumers'::regclass AND conname='api_consumers_environment_fkey') THEN
    ALTER TABLE api_consumers ADD CONSTRAINT api_consumers_environment_fkey
      FOREIGN KEY (environment_id) REFERENCES project_environments(id) ON DELETE CASCADE NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='consumer_keys'::regclass AND conname='consumer_keys_environment_fkey') THEN
    ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_environment_fkey
      FOREIGN KEY (environment_id) REFERENCES project_environments(id) ON DELETE CASCADE NOT VALID;
  END IF;
END $$;

-- A consumer's environment is one of its app's project environments and
-- never changes.
CREATE OR REPLACE FUNCTION guard_api_consumer_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.environment_id IS DISTINCT FROM OLD.environment_id THEN
    RAISE EXCEPTION 'an API consumer keeps its environment' USING ERRCODE = '23514';
  END IF;
  IF NEW.environment_id IS NOT NULL AND NOT EXISTS (
      SELECT 1 FROM apps a JOIN project_environments e ON e.project_id = a.project_id AND e.account_id = a.account_id
      WHERE a.id = NEW.app_id AND e.id = NEW.environment_id) THEN
    RAISE EXCEPTION 'an API consumer environment belongs to its app''s project' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS api_consumer_environment_guard ON api_consumers;
CREATE TRIGGER api_consumer_environment_guard BEFORE INSERT OR UPDATE ON api_consumers
  FOR EACH ROW EXECUTE FUNCTION guard_api_consumer_environment();

-- A key takes its consumer's environment; a key without a consumer is
-- production's.
CREATE OR REPLACE FUNCTION assign_consumer_key_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.consumer_id IS NULL THEN
    NEW.environment_id := NULL;
  ELSE
    SELECT c.environment_id INTO NEW.environment_id FROM api_consumers c WHERE c.id = NEW.consumer_id;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS consumer_key_environment ON consumer_keys;
CREATE TRIGGER consumer_key_environment BEFORE INSERT OR UPDATE OF consumer_id, environment_id ON consumer_keys
  FOR EACH ROW EXECUTE FUNCTION assign_consumer_key_environment();
-- The per-environment identities are built concurrently by
-- 20261125221809200_api_consumer_environment_indexes; the app-wide ones are
-- dropped by a contract migration one release later (N-1 inserts platform
-- tenant consumers ON CONFLICT (app_id, external_ref), and its gateway relies
-- on the app-wide key prefix).
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS consumer_key_environment ON consumer_keys;
DROP FUNCTION IF EXISTS assign_consumer_key_environment();
DROP TRIGGER IF EXISTS api_consumer_environment_guard ON api_consumers;
DROP FUNCTION IF EXISTS guard_api_consumer_environment();
DELETE FROM consumer_keys WHERE environment_id IS NOT NULL;
DELETE FROM api_consumers WHERE environment_id IS NOT NULL;
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_environment_fkey;
ALTER TABLE api_consumers DROP CONSTRAINT IF EXISTS api_consumers_environment_fkey;
ALTER TABLE consumer_keys DROP COLUMN IF EXISTS environment_id;
ALTER TABLE api_consumers DROP COLUMN IF EXISTS environment_id;
-- +goose StatementEnd
