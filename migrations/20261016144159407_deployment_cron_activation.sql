-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_cron_activations (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 upload_id text REFERENCES upload_sessions(id) ON DELETE SET NULL,
 session_id text NOT NULL CHECK (session_id ~ '^[0-9a-f]{32}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 revision bigint NOT NULL CHECK (revision>0),
 status text NOT NULL CHECK (status IN ('prepared','activated','conflict','discarded','canceled')),
 updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE deployment_cron_activations;
