-- filename: 20261027220959775_platform_mail_agent_run_delivered_indexes.sql
-- migration-compat: expand
-- Build without queuing behind ordinary outbox and delivery writes. Replacing
-- an interrupted build also removes the invalid index PostgreSQL leaves.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- One intent per agent delivery, and the pending payloads' key references.
DROP INDEX CONCURRENTLY IF EXISTS platform_mail_producer_outbox_agent_delivery_idx;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS platform_mail_producer_outbox_agent_delivery_idx
    ON platform_mail_producer_outbox (agent_delivery_id) WHERE template = 'agent_run_delivered';
DROP INDEX CONCURRENTLY IF EXISTS platform_mail_producer_outbox_agent_delivery_keys_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS platform_mail_producer_outbox_agent_delivery_keys_idx
    ON platform_mail_producer_outbox (namespace, producer_id, security_alert_key_id)
    WHERE template = 'agent_run_delivered' AND state IN ('queued', 'claimed');
-- The capture pass reads email deliveries in creation order.
DROP INDEX CONCURRENTLY IF EXISTS agent_deliveries_email_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS agent_deliveries_email_idx ON agent_deliveries (created_at, id)
    WHERE destination = 'email';
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS agent_deliveries_email_idx;
DROP INDEX CONCURRENTLY IF EXISTS platform_mail_producer_outbox_agent_delivery_keys_idx;
DROP INDEX CONCURRENTLY IF EXISTS platform_mail_producer_outbox_agent_delivery_idx;
RESET lock_timeout;
