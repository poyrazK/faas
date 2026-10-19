-- migration-compat: expand
-- filename: 20261019072505518_route_insight_delivery_index.sql

-- ADR-1198: one delivery per subscription and route insight transition. The
-- transition timestamp is part of the payload, so an insight that resolves
-- and returns is deliverable again while retried writes of one transition
-- stay idempotent. Built concurrently so app_webhook_deliveries keeps taking
-- writes; replacing an interrupted build also removes the invalid index.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS app_webhook_deliveries_route_insight_transition_uniq;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS app_webhook_deliveries_route_insight_transition_uniq
    ON app_webhook_deliveries (
        webhook_id,
        event,
        (payload ->> 'kind'),
        (payload ->> 'method'),
        (payload ->> 'route'),
        (payload ->> 'transition_id')
    )
    WHERE event IN ('routes.insight.detected', 'routes.insight.resolved');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS app_webhook_deliveries_route_insight_transition_uniq;
RESET lock_timeout;
