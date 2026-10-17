-- +goose Up
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
-- +goose StatementBegin
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
-- +goose StatementEnd
CREATE TRIGGER scenario_postgres_fixture_immutable BEFORE UPDATE ON scenario_postgres_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_postgres_fixture_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE status NOT IN ('complete','deleted')) THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL fixtures require cleanup before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_postgres_fixtures;
DROP FUNCTION scenario_postgres_fixture_immutable();
