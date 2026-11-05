-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE managed_postgres_live_login_attempts (
 database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
 attempted_at timestamptz NOT NULL CHECK (isfinite(attempted_at))
);
COMMENT ON TABLE managed_postgres_live_login_attempts IS
 'Last durable recovery attempt; untried and oldest attempts run first, independently of provider success.';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE managed_postgres_live_login_attempts;
