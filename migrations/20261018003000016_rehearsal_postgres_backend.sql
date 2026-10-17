-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_postgres_fixtures') AND tgname='scenario_postgres_backend_guard' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_postgres_fixtures') AND attname='backend_sha256' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_postgres_backend_guard()') AND md5(prosrc)=ANY(ARRAY['18ea76d9637c62b44a38582180b2a340'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_postgres_fixtures') AND tgname='scenario_postgres_backend_guard' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_postgres_fixtures') AND attname='backend_sha256' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_postgres_backend_guard()') AND md5(prosrc)=ANY(ARRAY['18ea76d9637c62b44a38582180b2a340'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 16; inspect recovery before continuing';
  END IF;

ALTER TABLE scenario_postgres_fixtures ADD COLUMN backend_sha256 text NOT NULL DEFAULT ''
 CHECK (backend_sha256='' OR backend_sha256 ~ '^[a-f0-9]{64}$');
CREATE FUNCTION scenario_postgres_backend_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.backend_sha256!~'^[a-f0-9]{64}$'
 OR TG_OP='UPDATE' AND NEW.backend_sha256 IS DISTINCT FROM OLD.backend_sha256 THEN
  RAISE EXCEPTION 'rehearsal PostgreSQL backend identity is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_postgres_backend_guard BEFORE INSERT OR UPDATE ON scenario_postgres_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_postgres_backend_guard();
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
DROP TRIGGER scenario_postgres_backend_guard ON scenario_postgres_fixtures;
DROP FUNCTION scenario_postgres_backend_guard();
ALTER TABLE scenario_postgres_fixtures DROP COLUMN backend_sha256;
