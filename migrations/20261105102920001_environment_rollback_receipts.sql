-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_environment_rollback_receipts (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 promotion_id uuid NOT NULL REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
 attempt_id text NOT NULL CHECK (attempt_id ~ '^[0-9a-f]{32}$'),
 request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
 promotion_hash text NOT NULL CHECK (promotion_hash ~ '^[0-9a-f]{64}$'),
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 from_environment_id uuid NOT NULL,
 environment_id uuid NOT NULL,
 accepted_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(accepted_at)),
 CHECK (from_environment_id <> environment_id),
 PRIMARY KEY (promotion_id),
 UNIQUE (account_id, attempt_id)
);

-- +goose Down
DROP TABLE project_environment_rollback_receipts;
