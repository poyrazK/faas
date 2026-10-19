-- migration-compat: expand
-- +goose Up
-- ADR-1321: a trigger may deliver each dispatch batch as one invocation.
-- 'record' keeps one invocation per record. cron has no records and queue
-- records carry durable per-record invocations, so both stay 'record'. The
-- previous release inserts without delivery and gets 'record'.
SET LOCAL lock_timeout = '5s';
ALTER TABLE triggers ADD COLUMN IF NOT EXISTS delivery text NOT NULL DEFAULT 'record';
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_delivery_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_delivery_check
    CHECK (delivery IN ('record','batch') AND (delivery = 'record' OR kind NOT IN ('cron','queue')));

-- +goose Down
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_delivery_check;
ALTER TABLE triggers DROP COLUMN IF EXISTS delivery;
