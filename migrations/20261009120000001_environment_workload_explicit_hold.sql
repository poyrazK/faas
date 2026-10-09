-- ADR-568: retain immutable candidate runtime inputs after graph activation.
-- +goose Up
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS environment_workload_held boolean NOT NULL DEFAULT false;
UPDATE deployments SET environment_workload_held=true WHERE environment_workload_runtime IS NOT NULL;

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='deployments'::regclass AND conname='deployments_environment_workload_held_requires_runtime') THEN
  ALTER TABLE deployments ADD CONSTRAINT deployments_environment_workload_held_requires_runtime
   CHECK(NOT environment_workload_held OR environment_workload_runtime IS NOT NULL);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Keep the explicit hold state on rollback; dropping it could reinterpret a
-- released candidate as held or an in-flight candidate as runnable.
SELECT 1;
