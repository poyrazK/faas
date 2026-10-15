-- +goose Up
-- +goose StatementBegin
-- ADR-1252. apid-only advisory intent; no production recovery worker reads it.
CREATE TABLE IF NOT EXISTS app_environment_route_budgets (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL,
 project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid REFERENCES project_environments(id) ON DELETE CASCADE,
 inherit boolean NOT NULL,
 enabled boolean NOT NULL,
 revision bigint NOT NULL CHECK (revision > 0 AND revision <= 9007199254740991),
 routes jsonb NOT NULL CHECK (jsonb_typeof(routes)='array' AND jsonb_array_length(routes)<=20),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (app_id, scope),
 CHECK ((scope='default' AND environment_id IS NULL AND project_id IS NULL) OR (scope<>'default' AND environment_id IS NOT NULL AND project_id IS NOT NULL)),
 CHECK (NOT inherit OR (NOT enabled AND routes='[]'::jsonb))
);
CREATE TABLE IF NOT EXISTS environment_route_budget_receipts (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 request_hash text NOT NULL CHECK (length(request_hash)=64),
 response jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(account_id, request_id)
);
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS environment_route_budget_receipts;
DROP TABLE IF EXISTS app_environment_route_budgets;
-- +goose StatementEnd
