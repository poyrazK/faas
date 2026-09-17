-- +goose Up
-- +goose StatementBegin

ALTER TABLE apps
  ADD COLUMN IF NOT EXISTS visibility text NOT NULL DEFAULT 'public';

ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_visibility_chk;
ALTER TABLE apps
  ADD CONSTRAINT apps_visibility_chk
  CHECK (visibility IN ('public', 'internal'));

CREATE INDEX IF NOT EXISTS apps_public_visibility_idx
  ON apps (status, visibility);

CREATE OR REPLACE FUNCTION apps_visibility_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.visibility IS DISTINCT FROM OLD.visibility THEN
    PERFORM pg_notify(
      'app_changed',
      json_build_object('kind', 'updated', 'app_id', NEW.id::text)::text
    );
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_visibility_notify_trg ON apps;
CREATE TRIGGER apps_visibility_notify_trg
AFTER UPDATE OF visibility ON apps
FOR EACH ROW
EXECUTE FUNCTION apps_visibility_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS apps_visibility_notify_trg ON apps;
DROP FUNCTION IF EXISTS apps_visibility_notify();
DROP INDEX IF EXISTS apps_public_visibility_idx;
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_visibility_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS visibility;

-- +goose StatementEnd
