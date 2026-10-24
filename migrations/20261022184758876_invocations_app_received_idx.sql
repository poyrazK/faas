-- migration-compat: expand
-- filename: 20261022184758876_invocations_app_received_idx.sql

-- ADR-20261022184757: the awake report reads an app's deliveries by when they
-- reached the instance. Built concurrently so invocations keep taking writes;
-- replacing an interrupted build also removes the invalid index it left.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS invocations_app_received_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_app_received_idx ON invocations (app_id, received_at)
    WHERE received_at IS NOT NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS invocations_app_received_idx;
RESET lock_timeout;
