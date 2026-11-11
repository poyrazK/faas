-- filename: 20261111081523417_scheduled_release_scheduler.sql
-- ADR-20261021152201 amendment (2026-11-10): a scheduled release starts only
-- while whoever scheduled it may still deploy the app. scheduled_by is a
-- display label (an email or api_key:<id>); these columns name the account
-- and the API key, if any, so the worker can check them at release time.
-- No foreign keys: a deleted account or key must read as gone, which blocks
-- the start, not as NULL, which reads as a schedule recorded before this
-- change. Rows recorded before it keep NULL and start as they did.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE scheduled_releases
  ADD COLUMN IF NOT EXISTS scheduled_by_account_id uuid,
  ADD COLUMN IF NOT EXISTS scheduled_by_key_id uuid;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE scheduled_releases
  DROP COLUMN IF EXISTS scheduled_by_key_id,
  DROP COLUMN IF EXISTS scheduled_by_account_id;
-- +goose StatementEnd
