-- migration-compat: expand
-- filename: 20261019111631574_account_deleted_app_inventory.sql

-- Account-owned deleted-app inventory (GET /v1/deleted-apps) pages each
-- account's tombstones newest first. Built concurrently so apps keeps taking
-- writes; replacing an interrupted build also removes the invalid index.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS apps_account_deleted_inventory_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS apps_account_deleted_inventory_idx
    ON apps (account_id, coalesce(deleted_at, created_at) DESC, (id::text) DESC)
    WHERE status = 'deleted';
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS apps_account_deleted_inventory_idx;
RESET lock_timeout;
