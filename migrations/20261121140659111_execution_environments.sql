-- filename: 20261121140659111_execution_environments.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Ownership survives account removal until imaged acknowledges physical GC.
CREATE TABLE IF NOT EXISTS execution_environments (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 principal_id text NOT NULL DEFAULT '',
 runtime text NOT NULL CHECK (runtime IN ('node22','node24','python312','python313')),
 recipe jsonb NOT NULL CHECK (octet_length(recipe::text) <= 2097152),
 recipe_digest text NOT NULL CHECK (recipe_digest ~ '^[0-9a-f]{64}$'),
 base_ref text NOT NULL,
 status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','building','built','imaging','ready','failed','retiring','deleted')),
 lease text NOT NULL DEFAULT '',
 lease_expires_at timestamptz,
 build_attempts integer NOT NULL DEFAULT 0 CHECK (build_attempts BETWEEN 0 AND 3),
 image_attempts integer NOT NULL DEFAULT 0 CHECK (image_attempts BETWEEN 0 AND 3),
 export_key text NOT NULL DEFAULT '',
 export_digest text NOT NULL DEFAULT '',
 base_key text NOT NULL DEFAULT '',
 image_digest text NOT NULL DEFAULT '',
 guest_executor_digest text NOT NULL DEFAULT '',
 artifact_keys text[] NOT NULL DEFAULT '{}',
 artifact_bytes bigint NOT NULL DEFAULT 0 CHECK (artifact_bytes BETWEEN 0 AND 1073741824),
 failure_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (status != 'ready' OR (image_digest ~ '^[0-9a-f]{64}$' AND guest_executor_digest ~ '^[0-9a-f]{64}$' AND base_key != '' AND artifact_bytes > 0))
);
CREATE UNIQUE INDEX IF NOT EXISTS execution_environments_recipe_unique ON execution_environments(account_id,principal_id,recipe_digest) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS execution_environments_pending ON execution_environments(status,created_at);
ALTER TABLE executions ADD COLUMN IF NOT EXISTS environment_id uuid;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_environment_id_fkey') THEN
  ALTER TABLE executions ADD CONSTRAINT executions_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES execution_environments(id) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
