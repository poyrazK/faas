-- +goose Up
-- migration-compat: expand
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS data_api_shared_budgets (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 budget_key text NOT NULL CHECK (budget_key ~ '^[a-f0-9]{64}$'),
 window_start timestamptz NOT NULL,
 window_end timestamptz NOT NULL CHECK (window_end > window_start),
 requests bigint NOT NULL CHECK (requests > 0),
 PRIMARY KEY(app_id, budget_key)
);
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  GRANT SELECT, INSERT, UPDATE, DELETE ON data_api_shared_budgets TO faas_runtime;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE data_api_shared_budgets;
