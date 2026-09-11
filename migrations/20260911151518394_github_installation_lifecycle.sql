-- +goose Up
-- +goose StatementBegin
-- Accept GitHub App access-lifecycle deliveries in the durable inbox. The
-- previous check was intentionally push/PR-only and would dead-letter an
-- uninstall or repository removal before githubd could revoke local access.
ALTER TABLE github_webhook_deliveries
  DROP CONSTRAINT IF EXISTS github_webhook_deliveries_event_type_check;
ALTER TABLE github_webhook_deliveries
  ADD CONSTRAINT github_webhook_deliveries_event_type_check
  CHECK (event_type IN (
    'push', 'pull_request', 'installation',
    'installation_repositories', 'repository'
  ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM github_webhook_deliveries
 WHERE event_type IN ('installation', 'installation_repositories', 'repository');
ALTER TABLE github_webhook_deliveries
  DROP CONSTRAINT IF EXISTS github_webhook_deliveries_event_type_check;
ALTER TABLE github_webhook_deliveries
  ADD CONSTRAINT github_webhook_deliveries_event_type_check
  CHECK (event_type IN ('push', 'pull_request'));
-- +goose StatementEnd
