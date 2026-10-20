-- migration-compat: expand
-- ADR-20261018183000: a running Operation starts child Operations for its
-- customer. One row per child names its parent and the parent's key for it;
-- the key makes a retried parent replay the child instead of starting another.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS customer_operation_children (
    parent_operation_id uuid        NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
    child_key           text        NOT NULL CHECK (octet_length(child_key) BETWEEN 1 AND 64),
    child_operation_id  uuid        NOT NULL UNIQUE REFERENCES customer_operations(id) ON DELETE CASCADE,
    account_id          uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_at          timestamptz NOT NULL,
    PRIMARY KEY (parent_operation_id, child_key),
    CHECK (parent_operation_id <> child_operation_id)
);

-- +goose Down
DROP TABLE IF EXISTS customer_operation_children;
