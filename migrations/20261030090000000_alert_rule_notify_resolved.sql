-- filename: 20261030090000000_alert_rule_notify_resolved.sql
-- migration-compat: expand

-- +goose Up
-- ADR-20261029203000: a rule may also send its webhook a signed resolve event
-- when it recovers. Off by default: receivers written before it count every
-- delivery as a fire. A constant default adds the column without a rewrite.
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS notify_resolved boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE alert_rules DROP COLUMN IF EXISTS notify_resolved;
