-- migration-compat: expand
-- ADR-20261018210500: admission and the queue releaser count a customer's
-- in-flight Operations. Built concurrently so customer_operations keeps taking
-- writes; replacing an interrupted build also removes the invalid index
-- PostgreSQL leaves behind.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS customer_operations_tenant_in_flight_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS customer_operations_tenant_in_flight_idx
    ON customer_operations (platform_tenant_id, app_id)
    WHERE state IN ('accepted', 'running', 'requires_reconciliation');
RESET lock_timeout;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS customer_operations_tenant_in_flight_idx;
