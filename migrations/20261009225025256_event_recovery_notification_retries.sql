-- filename: 20261009225025256_event_recovery_notification_retries.sql
-- +goose Up
ALTER TABLE event_recovery_jobs ADD COLUMN notification_retry_receipts jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_notification_retry_receipts_object_chk CHECK (jsonb_typeof(notification_retry_receipts)='object');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE notification_retry_receipts<>'{}'::jsonb) THEN RAISE EXCEPTION 'Retained notification retry decisions must expire before downgrade'; END IF; END $$;
ALTER TABLE event_recovery_jobs DROP COLUMN notification_retry_receipts;
-- +goose StatementEnd
