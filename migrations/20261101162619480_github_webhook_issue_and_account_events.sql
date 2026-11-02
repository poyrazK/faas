-- filename: 20261101162619480_github_webhook_issue_and_account_events.sql

-- ADR-20261101160300: the durable GitHub webhook inbox accepts `issues`
-- deliveries, which githubd already enqueues for issue-tracker callbacks
-- (ADR-1476) and which the check refused with a 500, and
-- `installation_target`, which reports a renamed user or organization.
-- Widening the check is additive; an older githubd writes a subset.
-- migration-compat: expand
--
-- The wider check is added NOT VALID under a new name, validated, and only
-- then is the old one dropped, each in its own statement, so deliveries keep
-- being written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $events$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'github_webhook_deliveries'::regclass AND conname = 'github_webhook_deliveries_event_type_chk') THEN
    ALTER TABLE github_webhook_deliveries ADD CONSTRAINT github_webhook_deliveries_event_type_chk
      CHECK (event_type IN (
        'push', 'pull_request', 'installation',
        'installation_repositories', 'repository',
        'issues', 'installation_target'
      )) NOT VALID;
  END IF;
END
$events$;
-- +goose StatementEnd

ALTER TABLE github_webhook_deliveries VALIDATE CONSTRAINT github_webhook_deliveries_event_type_chk;

ALTER TABLE github_webhook_deliveries DROP CONSTRAINT IF EXISTS github_webhook_deliveries_event_type_check;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM github_webhook_deliveries
 WHERE event_type IN ('issues', 'installation_target');

-- +goose StatementBegin
DO $events$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'github_webhook_deliveries'::regclass AND conname = 'github_webhook_deliveries_event_type_check') THEN
    ALTER TABLE github_webhook_deliveries ADD CONSTRAINT github_webhook_deliveries_event_type_check
      CHECK (event_type IN (
        'push', 'pull_request', 'installation',
        'installation_repositories', 'repository'
      )) NOT VALID;
  END IF;
END
$events$;
-- +goose StatementEnd

ALTER TABLE github_webhook_deliveries VALIDATE CONSTRAINT github_webhook_deliveries_event_type_check;

ALTER TABLE github_webhook_deliveries DROP CONSTRAINT IF EXISTS github_webhook_deliveries_event_type_chk;
