-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Completed evidence can follow its owning app/account's retention lifecycle.
-- An active fixture must retain its cleanup journal until deletion is confirmed.
ALTER TABLE scenario_postgres_fixtures DROP CONSTRAINT scenario_postgres_fixtures_account_id_fkey;
ALTER TABLE scenario_postgres_fixtures DROP CONSTRAINT scenario_postgres_fixtures_app_id_fkey;
ALTER TABLE scenario_postgres_fixtures ADD CONSTRAINT scenario_postgres_fixtures_account_id_fkey
 FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE scenario_postgres_fixtures ADD CONSTRAINT scenario_postgres_fixtures_app_id_fkey
 FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE CASCADE;
-- +goose StatementBegin
CREATE FUNCTION scenario_postgres_fixture_delete_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status NOT IN ('complete','deleted') THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL cleanup must be confirmed before deleting its journal' USING ERRCODE='23514';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_postgres_fixture_delete_guard BEFORE DELETE ON scenario_postgres_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_postgres_fixture_delete_guard();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE status NOT IN ('complete','deleted')) THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL fixtures require cleanup before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_postgres_fixture_delete_guard ON scenario_postgres_fixtures;
DROP FUNCTION scenario_postgres_fixture_delete_guard();
ALTER TABLE scenario_postgres_fixtures DROP CONSTRAINT scenario_postgres_fixtures_account_id_fkey;
ALTER TABLE scenario_postgres_fixtures DROP CONSTRAINT scenario_postgres_fixtures_app_id_fkey;
ALTER TABLE scenario_postgres_fixtures ADD CONSTRAINT scenario_postgres_fixtures_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id);
ALTER TABLE scenario_postgres_fixtures ADD CONSTRAINT scenario_postgres_fixtures_app_id_fkey FOREIGN KEY(app_id) REFERENCES apps(id);
