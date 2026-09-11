-- filename: 20260911154838591_github_installation_sync.sql

-- +goose Up
-- +goose StatementBegin

-- Persist the last installation reconciliation outcome. Keeping this beside
-- the install row makes drift health visible after a githubd restart and
-- gives operators a bounded error string without storing repository data.
ALTER TABLE github_installations
  ADD COLUMN IF NOT EXISTS last_reconciled_at timestamptz,
  ADD COLUMN IF NOT EXISTS last_reconcile_error text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_reconcile_repository_count integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_reconcile_detached_count integer NOT NULL DEFAULT 0;

ALTER TABLE github_installations
  DROP CONSTRAINT IF EXISTS github_installations_reconcile_repository_count_ck,
  DROP CONSTRAINT IF EXISTS github_installations_reconcile_detached_count_ck;
ALTER TABLE github_installations
  ADD CONSTRAINT github_installations_reconcile_repository_count_ck
    CHECK (last_reconcile_repository_count >= 0),
  ADD CONSTRAINT github_installations_reconcile_detached_count_ck
    CHECK (last_reconcile_detached_count >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE github_installations
  DROP CONSTRAINT IF EXISTS github_installations_reconcile_repository_count_ck,
  DROP CONSTRAINT IF EXISTS github_installations_reconcile_detached_count_ck;
ALTER TABLE github_installations
  DROP COLUMN IF EXISTS last_reconcile_detached_count,
  DROP COLUMN IF EXISTS last_reconcile_repository_count,
  DROP COLUMN IF EXISTS last_reconcile_error,
  DROP COLUMN IF EXISTS last_reconciled_at;
-- +goose StatementEnd
