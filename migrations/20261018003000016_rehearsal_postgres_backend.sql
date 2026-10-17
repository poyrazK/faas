-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_postgres_fixtures ADD COLUMN backend_sha256 text NOT NULL DEFAULT ''
 CHECK (backend_sha256='' OR backend_sha256 ~ '^[a-f0-9]{64}$');
-- +goose StatementBegin
CREATE FUNCTION scenario_postgres_backend_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.backend_sha256!~'^[a-f0-9]{64}$'
 OR TG_OP='UPDATE' AND NEW.backend_sha256 IS DISTINCT FROM OLD.backend_sha256 THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL backend identity is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_postgres_backend_guard BEFORE INSERT OR UPDATE ON scenario_postgres_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_postgres_backend_guard();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE status NOT IN ('complete','deleted')) THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL fixtures require cleanup before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_postgres_backend_guard ON scenario_postgres_fixtures;
DROP FUNCTION scenario_postgres_backend_guard();
ALTER TABLE scenario_postgres_fixtures DROP COLUMN backend_sha256;
