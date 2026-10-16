-- +goose Up
CREATE INDEX apps_account_deleted_inventory_idx
    ON apps (account_id, coalesce(deleted_at, created_at) DESC, (id::text) DESC)
    WHERE status = 'deleted';

-- +goose Down
DROP INDEX apps_account_deleted_inventory_idx;
