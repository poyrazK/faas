-- filename: 20261021183000602_app_webhook_outbox_consumer_events_validate.sql

-- ADR-20261021183000: app webhook events about customers acting on their
-- own account go through the outbox, and so does consumer.usage_threshold,
-- which the outbox event check never allowed: in Postgres a usage alert for
-- an app with a subscribed webhook failed. The check is replaced online in
-- three steps: this one validates the wider check without blocking writes.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhook_event_outbox VALIDATE CONSTRAINT app_webhook_event_outbox_event_v2_chk;

-- +goose Down
SELECT 1;
