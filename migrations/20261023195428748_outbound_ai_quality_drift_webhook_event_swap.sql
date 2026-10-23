-- filename: 20261023195428748_outbound_ai_quality_drift_webhook_event_swap.sql
-- ADR 20261021171712: last of three steps widening the app webhook outbox
-- event check for outbound.ai_quality.drift: drops the old check and gives
-- the wider one its name, which later migrations widening the vocabulary
-- look up. Nothing to do when the first step found the event already
-- allowed.
-- migration-compat: contract

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass
            AND conname='app_webhook_event_outbox_event_drift_chk') THEN
  ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
  ALTER TABLE app_webhook_event_outbox RENAME CONSTRAINT app_webhook_event_outbox_event_drift_chk TO app_webhook_event_outbox_event_chk;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Retain the event vocabulary for queued deliveries.
SELECT 1;
