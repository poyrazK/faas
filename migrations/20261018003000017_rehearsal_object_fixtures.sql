-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE scenario_object_fixtures (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK (run_id ~ '^[a-f0-9]{32}$'),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 status text NOT NULL CHECK (status IN ('ready','sealing','complete','deleted')),
 expires_at timestamptz NOT NULL,
 document jsonb NOT NULL CHECK (pg_column_size(document) <= 2097152),
 UNIQUE(account_id,run_id),
 CHECK (document->>'Status'=status),
 CHECK (document->>'ID'=id::text AND document->>'AccountID'=account_id::text
  AND document->>'RunID'=run_id AND document->>'AppID'=app_id::text)
);
CREATE INDEX scenario_object_fixtures_expiry ON scenario_object_fixtures(expires_at)
 WHERE status IN ('ready','sealing');
-- +goose StatementBegin
CREATE FUNCTION scenario_object_fixture_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.status NOT IN ('complete','deleted') THEN
   RAISE EXCEPTION 'object fixture requires cleanup before purge' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 IF (NEW.id,NEW.account_id,NEW.run_id,NEW.app_id,NEW.expires_at)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.run_id,OLD.app_id,OLD.expires_at)
 OR (NEW.document - ARRAY['Status','Current','Attempts','Failed','Evidence'])
   IS DISTINCT FROM (OLD.document - ARRAY['Status','Current','Attempts','Failed','Evidence'])
 OR (OLD.status IN ('complete','deleted') AND NEW IS DISTINCT FROM OLD)
 OR NOT (NEW.status=OLD.status OR (OLD.status='ready' AND NEW.status='sealing')
   OR (OLD.status='sealing' AND NEW.status='complete') OR NEW.status='deleted')
 OR ((OLD.document->>'Failed')::boolean AND NOT (NEW.document->>'Failed')::boolean)
 OR (NEW.status IN ('complete','deleted') AND jsonb_array_length(NEW.document->'Current')<>0)
 THEN RAISE EXCEPTION 'object fixture journal is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_object_fixture_immutable BEFORE UPDATE OR DELETE ON scenario_object_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_object_fixture_immutable();
-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_object_fixtures WHERE status NOT IN ('complete','deleted')) THEN
  RAISE EXCEPTION 'object fixture cleanup is required before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_object_fixtures;
DROP FUNCTION scenario_object_fixture_immutable();
