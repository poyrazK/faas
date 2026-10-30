-- filename: 20261030090000002_alert_deliveries_fired_idx.sql
-- migration-compat: expand
-- ADR-20261029203000: meterd purges alert deliveries past
-- AlertDeliveryRetentionDays in batches, oldest first; this index finds them
-- without scanning the table. Built without blocking writes; replacing an
-- interrupted build also removes the invalid index PostgreSQL leaves.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS alert_deliveries_fired_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS alert_deliveries_fired_idx ON alert_deliveries (fired_at);
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS alert_deliveries_fired_idx;
RESET lock_timeout;
