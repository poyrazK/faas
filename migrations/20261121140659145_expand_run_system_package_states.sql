-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'execution_environments'::regclass AND conname = 'execution_environments_system_status_check') THEN
  ALTER TABLE execution_environments ADD CONSTRAINT execution_environments_system_status_check CHECK (status IN ('queued','building','built','imaging','ready','failed','retiring','deleted','queued_system_v1','building_system_v1','built_system_v1','imaging_system_v1')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
