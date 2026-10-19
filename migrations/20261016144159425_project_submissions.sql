-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_submissions (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 attempt_id text NOT NULL CHECK (attempt_id ~ '^[0-9a-f]{32}$'),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 payload jsonb NOT NULL,
 status text NOT NULL CHECK (status IN ('processing','complete','rejected','interrupted')),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (account_id,attempt_id)
);
-- +goose Down
DROP TABLE project_submissions;
