-- filename: 20261016153050927_environment_cache_policies.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_cache_policies (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK (revision > 0 AND revision <= 9007199254740991),
 rules jsonb NOT NULL CHECK (jsonb_typeof(rules)='array' AND jsonb_array_length(rules)<=20),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(app_id,environment_id)
);
CREATE TABLE IF NOT EXISTS environment_cache_policy_receipts (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 request_hash text NOT NULL,
 response jsonb NOT NULL,
 PRIMARY KEY(account_id,request_id)
);

-- +goose Down
DROP TABLE environment_cache_policy_receipts;
DROP TABLE project_environment_cache_policies;
