-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_promotion_receipts (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 attempt_id text NOT NULL CHECK (attempt_id ~ '^[0-9a-f]{32}$'),
 request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 expected_serving_deployment_id text NOT NULL CHECK (expected_serving_deployment_id = '' OR expected_serving_deployment_id ~ '^[0-9a-f-]{36}$'),
 scope text NOT NULL CHECK (scope = 'default' OR scope ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$'),
 project_id text NOT NULL CHECK (project_id = '' OR project_id ~ '^[0-9a-f-]{36}$'),
 environment_id text NOT NULL CHECK (environment_id = '' OR environment_id ~ '^[0-9a-f-]{36}$'),
 accepted_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(accepted_at)),
 CHECK ((scope = 'default' AND project_id = '' AND environment_id = '') OR (scope <> 'default' AND project_id <> '' AND environment_id <> '')),
 PRIMARY KEY (app_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS deployment_promotion_receipts_deployment_idx ON deployment_promotion_receipts (deployment_id);

-- +goose Down
DROP TABLE deployment_promotion_receipts;
