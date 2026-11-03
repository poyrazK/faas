-- filename: 20261103093241470_issue_event_usage.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A NULL-free, lazily initialized count avoids a table-wide migration. Until
-- initialization, trigger deltas are ignored and the first writer counts the
-- existing rows under the usage-row lock.
CREATE TABLE IF NOT EXISTS issue_event_usage (
 app_id uuid PRIMARY KEY,
 retained_count bigint NOT NULL DEFAULT 0 CHECK (retained_count >= 0),
 initialized boolean NOT NULL DEFAULT false
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION issue_event_usage_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO issue_event_usage (app_id, retained_count, initialized)
 SELECT n.app_id, count(*)::bigint, false FROM inserted_issue_events n JOIN apps a ON a.id=n.app_id
 GROUP BY n.app_id ORDER BY n.app_id
 ON CONFLICT (app_id) DO UPDATE SET retained_count=CASE WHEN issue_event_usage.initialized
  THEN issue_event_usage.retained_count+EXCLUDED.retained_count ELSE issue_event_usage.retained_count END;
 RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION issue_event_usage_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO issue_event_usage (app_id, retained_count, initialized)
 SELECT d.app_id, count(*)::bigint, false FROM deleted_issue_events d JOIN apps a ON a.id=d.app_id
 GROUP BY d.app_id ORDER BY d.app_id
 ON CONFLICT (app_id) DO UPDATE SET retained_count=CASE WHEN issue_event_usage.initialized
  THEN issue_event_usage.retained_count-EXCLUDED.retained_count ELSE issue_event_usage.retained_count END;
 RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION issue_event_usage_app_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM issue_event_usage WHERE app_id=OLD.id;
 RETURN OLD;
END $$;
-- +goose StatementEnd
-- Preserve existing triggers on a ledger replay instead of recreating or
-- replacing a possibly newer definition.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'issue_events'::regclass
                AND tgname = 'issue_event_usage_insert' AND NOT tgisinternal) THEN
  CREATE TRIGGER issue_event_usage_insert AFTER INSERT ON issue_events
   REFERENCING NEW TABLE AS inserted_issue_events FOR EACH STATEMENT EXECUTE FUNCTION issue_event_usage_insert();
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'issue_events'::regclass
                AND tgname = 'issue_event_usage_delete' AND NOT tgisinternal) THEN
  CREATE TRIGGER issue_event_usage_delete AFTER DELETE ON issue_events
   REFERENCING OLD TABLE AS deleted_issue_events FOR EACH STATEMENT EXECUTE FUNCTION issue_event_usage_delete();
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'apps'::regclass
                AND tgname = 'issue_event_usage_app_delete' AND NOT tgisinternal) THEN
  CREATE TRIGGER issue_event_usage_app_delete AFTER DELETE ON apps
   FOR EACH ROW EXECUTE FUNCTION issue_event_usage_app_delete();
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS issue_event_usage_app_delete ON apps;
DROP TRIGGER IF EXISTS issue_event_usage_delete ON issue_events;
DROP TRIGGER IF EXISTS issue_event_usage_insert ON issue_events;
DROP FUNCTION IF EXISTS issue_event_usage_app_delete();
DROP FUNCTION IF EXISTS issue_event_usage_delete();
DROP FUNCTION IF EXISTS issue_event_usage_insert();
DROP TABLE IF EXISTS issue_event_usage;
