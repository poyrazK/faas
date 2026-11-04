-- filename: 20261023195423851_outbound_ai_quality_drift_webhook_event_validate.sql
-- ADR 20261021171712: second of three steps widening the app webhook outbox
-- event check for outbound.ai_quality.drift: validates the wider check
-- without blocking writes. Nothing to do when the first step found the
-- event already allowed.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass
            AND conname='app_webhook_event_outbox_event_drift_chk') THEN
  ALTER TABLE app_webhook_event_outbox VALIDATE CONSTRAINT app_webhook_event_outbox_event_drift_chk;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Validation leaves nothing to undo.
SELECT 1;
