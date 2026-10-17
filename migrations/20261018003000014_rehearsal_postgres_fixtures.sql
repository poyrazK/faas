-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_postgres_fixtures') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_postgres_fixtures') AND tgname='scenario_postgres_fixture_immutable' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_postgres_fixture_immutable()') AND md5(prosrc)=ANY(ARRAY['193290971d3e6f060b5e94381ad1fbbd'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_postgres_fixtures') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_postgres_fixtures') AND tgname='scenario_postgres_fixture_immutable' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_postgres_fixture_immutable()') AND md5(prosrc)=ANY(ARRAY['193290971d3e6f060b5e94381ad1fbbd'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 14; inspect recovery before continuing';
  END IF;

CREATE TABLE scenario_postgres_fixtures (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 run_id text NOT NULL CHECK (run_id ~ '^[a-f0-9]{32}$'),
 app_id uuid NOT NULL REFERENCES apps(id),
 owner_id uuid NOT NULL,
 database_name text NOT NULL UNIQUE CHECK (database_name ~ '^grpg_[a-f0-9]{32}$'),
 role_name text NOT NULL UNIQUE CHECK (role_name ~ '^grpg_[a-f0-9]{32}$'),
 status text NOT NULL CHECK (status IN ('provisioning','ready','sealing','complete','failed','deleted')),
 spec jsonb NOT NULL,
 initial_snapshot jsonb NOT NULL DEFAULT '[]',
 evidence jsonb NOT NULL DEFAULT '{}',
 expires_at timestamptz NOT NULL,
 UNIQUE(account_id,run_id)
);
CREATE FUNCTION scenario_postgres_fixture_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.account_id,NEW.run_id,NEW.app_id,NEW.owner_id,NEW.database_name,NEW.role_name,NEW.spec,NEW.expires_at)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.run_id,OLD.app_id,OLD.owner_id,OLD.database_name,OLD.role_name,OLD.spec,OLD.expires_at)
 OR NOT ((OLD.status='provisioning' AND NEW.status IN ('ready','failed','deleted'))
 OR (OLD.status='ready' AND NEW.status IN ('sealing','failed','deleted'))
 OR (OLD.status='sealing' AND NEW.status IN ('complete','failed','deleted'))
 OR (OLD.status='failed' AND NEW.status='deleted'))
 OR (OLD.status<>'provisioning' AND NEW.initial_snapshot IS DISTINCT FROM OLD.initial_snapshot)
 OR (NEW.status<>'complete' AND NEW.evidence IS DISTINCT FROM OLD.evidence) THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL journal is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_postgres_fixture_immutable BEFORE UPDATE ON scenario_postgres_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_postgres_fixture_immutable();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE status NOT IN ('complete','deleted')) THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL fixtures require cleanup before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_postgres_fixtures;
DROP FUNCTION scenario_postgres_fixture_immutable();
