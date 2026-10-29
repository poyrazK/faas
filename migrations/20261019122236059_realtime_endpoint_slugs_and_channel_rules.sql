-- migration-compat: expand
-- ADR-20261019102657: an endpoint may carry a slug, unique within its app, so
-- gregale.yaml can declare it and reconcile it across deploys and accounts,
-- and channel rules realtimed evaluates from verified token claims instead of
-- calling the authorize callback. API-created endpoints may stay without
-- either; the previous release never reads or writes the columns.
--
-- Each statement is its own short transaction (NO TRANSACTION): the columns
-- are added bare (the constant default adds no rewrite), their CHECKs are
-- added NOT VALID and validated separately, and the unique index is built
-- concurrently, so managed_realtime_endpoints keeps taking writes. Every step
-- is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE managed_realtime_endpoints ADD COLUMN IF NOT EXISTS slug text;

-- +goose StatementBegin
DO $slug$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_endpoints'::regclass
                 AND conname = 'managed_realtime_endpoints_slug_check') THEN
    ALTER TABLE managed_realtime_endpoints ADD CONSTRAINT managed_realtime_endpoints_slug_check
      CHECK (slug IS NULL OR slug ~ '^[a-z0-9][a-z0-9-]{0,47}$') NOT VALID;
  END IF;
END
$slug$;
-- +goose StatementEnd

ALTER TABLE managed_realtime_endpoints VALIDATE CONSTRAINT managed_realtime_endpoints_slug_check;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS managed_realtime_endpoints_app_slug
    ON managed_realtime_endpoints (app_id, slug) WHERE slug IS NOT NULL;

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('managed_realtime_endpoints_app_slug')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'managed_realtime_endpoints_app_slug is invalid: DROP INDEX CONCURRENTLY managed_realtime_endpoints_app_slug and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

ALTER TABLE managed_realtime_endpoints ADD COLUMN IF NOT EXISTS channel_rules jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose StatementBegin
DO $rules$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_endpoints'::regclass
                 AND conname = 'managed_realtime_endpoints_channel_rules_check') THEN
    ALTER TABLE managed_realtime_endpoints ADD CONSTRAINT managed_realtime_endpoints_channel_rules_check
      CHECK (jsonb_typeof(channel_rules) = 'array' AND jsonb_array_length(channel_rules) <= 32) NOT VALID;
  END IF;
END
$rules$;
-- +goose StatementEnd

ALTER TABLE managed_realtime_endpoints VALIDATE CONSTRAINT managed_realtime_endpoints_channel_rules_check;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE managed_realtime_endpoints DROP COLUMN IF EXISTS channel_rules;
DROP INDEX CONCURRENTLY IF EXISTS managed_realtime_endpoints_app_slug;
ALTER TABLE managed_realtime_endpoints DROP COLUMN IF EXISTS slug;
RESET lock_timeout;
