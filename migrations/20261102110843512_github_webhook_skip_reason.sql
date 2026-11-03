-- filename: 20261102110843512_github_webhook_skip_reason.sql

-- ADR-20261101160300 amendment 2: a GitHub delivery githubd completes without
-- deploying records why (an app that deploys from GitHub Actions, a branch
-- nothing deploys, a skip marker), so `gregale github status` can say it.
-- Empty means the delivery did its work. Only githubd writes the column; an
-- older githubd leaves it empty.
-- migration-compat: expand
--
-- The CHECK is added NOT VALID and validated in its own statement, so
-- deliveries keep being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE github_webhook_deliveries ADD COLUMN IF NOT EXISTS skip_reason text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $skip$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'github_webhook_deliveries'::regclass AND conname = 'github_webhook_deliveries_skip_reason_chk') THEN
    ALTER TABLE github_webhook_deliveries ADD CONSTRAINT github_webhook_deliveries_skip_reason_chk
      CHECK (skip_reason ~ '^[a-z_]{0,64}$') NOT VALID;
  END IF;
END
$skip$;
-- +goose StatementEnd

ALTER TABLE github_webhook_deliveries VALIDATE CONSTRAINT github_webhook_deliveries_skip_reason_chk;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE github_webhook_deliveries DROP CONSTRAINT IF EXISTS github_webhook_deliveries_skip_reason_chk;
ALTER TABLE github_webhook_deliveries DROP COLUMN IF EXISTS skip_reason;
