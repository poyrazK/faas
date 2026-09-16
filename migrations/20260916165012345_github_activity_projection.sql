-- +goose Up
-- +goose StatementBegin

-- Safe correlation fields for the customer-facing GitHub activity projection.
-- The raw webhook payload remains private to githubd; these fields are only
-- the bounded identity needed to join a delivery to the currently bound app.
ALTER TABLE github_webhook_deliveries
  ADD COLUMN IF NOT EXISTS installation_id bigint,
  ADD COLUMN IF NOT EXISTS repo_full_name text,
  ADD COLUMN IF NOT EXISTS commit_sha text;

CREATE INDEX IF NOT EXISTS github_webhook_deliveries_activity_idx
  ON github_webhook_deliveries (installation_id, repo_full_name, received_at DESC)
  WHERE installation_id IS NOT NULL AND repo_full_name IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS github_webhook_deliveries_activity_idx;
ALTER TABLE github_webhook_deliveries
  DROP COLUMN IF EXISTS commit_sha,
  DROP COLUMN IF EXISTS repo_full_name,
  DROP COLUMN IF EXISTS installation_id;
-- +goose StatementEnd
