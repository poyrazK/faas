-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- An already-started handoff may have sent a promotion before this column
-- existed. Default those rows to fenced and let new handoffs record intent.
ALTER TABLE native_postgres_base_refreshes
    ADD COLUMN IF NOT EXISTS promotion_started boolean NOT NULL DEFAULT true;
ALTER TABLE native_postgres_base_refreshes
    ALTER COLUMN promotion_started SET DEFAULT false;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE native_postgres_base_refreshes DROP COLUMN IF EXISTS promotion_started;
