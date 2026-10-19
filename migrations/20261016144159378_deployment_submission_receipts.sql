-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_submission_receipts (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    attempt_id text NOT NULL CHECK (attempt_id ~ '^[0-9a-f]{32}$'),
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    accepted_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(accepted_at)),
    PRIMARY KEY (app_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS deployment_submission_receipts_deployment_idx ON deployment_submission_receipts (deployment_id);

-- +goose Down
DROP TABLE deployment_submission_receipts;
