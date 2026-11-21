-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS execution_environment_diagnostics (
 environment_id uuid PRIMARY KEY REFERENCES execution_environments(id) ON DELETE CASCADE,
 diagnostic_sequence bigint NOT NULL,
 diagnostics jsonb NOT NULL,
 CONSTRAINT execution_environment_diagnostics_shape CHECK (diagnostic_sequence>=1 AND jsonb_typeof(diagnostics)='array' AND jsonb_array_length(diagnostics)<=128 AND octet_length(diagnostics::text)<=524288)
);
-- +goose StatementBegin
DO $guard$
BEGIN
 IF to_regprocedure('erase_retired_environment_diagnostics()') IS NULL THEN
CREATE FUNCTION erase_retired_environment_diagnostics() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='deleted' THEN DELETE FROM execution_environment_diagnostics WHERE environment_id=NEW.id; END IF;
 RETURN NEW;
END;
$$;
 END IF;
END $guard$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'execution_environments'::regclass AND tgname = 'erase_retired_environment_diagnostics') THEN
  CREATE TRIGGER erase_retired_environment_diagnostics AFTER UPDATE OF status ON execution_environments FOR EACH ROW EXECUTE FUNCTION erase_retired_environment_diagnostics();
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
