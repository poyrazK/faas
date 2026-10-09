-- filename: 20261009225025213_event_recovery_notification_receipts.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_recovery_jobs ADD COLUMN IF NOT EXISTS notification_receipts jsonb NOT NULL DEFAULT '{}'::jsonb;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_jobs'::regclass AND conname='event_recovery_jobs_notification_receipts_chk') THEN
 ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_notification_receipts_chk
 CHECK (jsonb_typeof(notification_receipts)='object' AND notification_receipts-ARRAY['event_recovery.completed','event_recovery.cancelled','event_recovery.expired','event_recovery.execution_finished']::text[]='{}'::jsonb);
 END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
ALTER TABLE event_recovery_jobs DROP COLUMN notification_receipts;
