-- +goose Up
ALTER TABLE runtime_snapshots ADD COLUMN IF NOT EXISTS profile text NOT NULL DEFAULT 'standard';
ALTER TABLE executions ADD COLUMN IF NOT EXISTS profile text NOT NULL DEFAULT 'standard';
ALTER TABLE executions ADD COLUMN IF NOT EXISTS runtime_image_digest text;

-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'runtime_snapshots'::regclass AND conname = 'runtime_snapshots_profile_check') THEN
  ALTER TABLE runtime_snapshots ADD CONSTRAINT runtime_snapshots_profile_check CHECK (
   profile = 'standard' OR (profile = 'python-data-v1' AND runtime = 'python313')
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_profile_check') THEN
  ALTER TABLE executions ADD CONSTRAINT executions_profile_check CHECK (
   profile = 'standard' OR (profile = 'python-data-v1' AND runtime = 'python313')
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_runtime_image_digest_check') THEN
  ALTER TABLE executions ADD CONSTRAINT executions_runtime_image_digest_check CHECK (
   runtime_image_digest IS NULL OR runtime_image_digest ~ '^sha256:[a-f0-9]{64}$'
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_profile_pin_check') THEN
  ALTER TABLE executions ADD CONSTRAINT executions_profile_pin_check CHECK (
   profile = 'standard' OR status NOT IN ('running','succeeded') OR runtime_image_digest IS NOT NULL
  );
 END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_execution_profile_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.profile IS DISTINCT FROM OLD.profile THEN
  RAISE EXCEPTION 'execution profile is immutable' USING ERRCODE = '23514';
 END IF;
 IF TG_TABLE_NAME = 'executions' THEN
  IF NEW.runtime_image_digest IS DISTINCT FROM OLD.runtime_image_digest AND
     (OLD.status <> 'restoring' OR OLD.runtime_image_digest IS NOT NULL) THEN
   RAISE EXCEPTION 'execution image digest is immutable after selection' USING ERRCODE = '23514';
  END IF;
 END IF;
 RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS executions_profile_identity ON executions;
CREATE TRIGGER executions_profile_identity BEFORE UPDATE ON executions
 FOR EACH ROW EXECUTE FUNCTION enforce_execution_profile_identity();
DROP TRIGGER IF EXISTS runtime_snapshots_profile_identity ON runtime_snapshots;
CREATE TRIGGER runtime_snapshots_profile_identity BEFORE UPDATE ON runtime_snapshots
 FOR EACH ROW EXECUTE FUNCTION enforce_execution_profile_identity();

-- +goose Down
DROP TRIGGER executions_profile_identity ON executions;
DROP TRIGGER runtime_snapshots_profile_identity ON runtime_snapshots;
DROP FUNCTION enforce_execution_profile_identity();
ALTER TABLE executions DROP COLUMN profile;
ALTER TABLE executions DROP COLUMN runtime_image_digest;
ALTER TABLE runtime_snapshots DROP COLUMN profile;
