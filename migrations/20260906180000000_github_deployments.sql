-- +goose Up
-- +goose StatementBegin
-- Stable identity mapping for the GitHub Deployments API. Check Runs remain
-- useful for build-gate UX, but GitHub's deployment timeline and downstream
-- deployment-status consumers need one provider deployment per Gregale
-- deployment, with later phases posted as statuses on that same object.
CREATE TABLE IF NOT EXISTS github_deployments (
  deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
  github_deployment_id bigint NOT NULL CHECK (github_deployment_id > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS github_deployments_provider_id_uidx
  ON github_deployments (github_deployment_id);

CREATE INDEX IF NOT EXISTS github_deployments_updated_idx
  ON github_deployments (updated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS github_deployments_updated_idx;
DROP INDEX IF EXISTS github_deployments_provider_id_uidx;
DROP TABLE IF EXISTS github_deployments;
-- +goose StatementEnd
