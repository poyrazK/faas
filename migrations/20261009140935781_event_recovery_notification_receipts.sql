-- filename: 20261009140935781_event_recovery_notification_receipts.sql

-- +goose Up
ALTER TABLE event_recovery_jobs ADD COLUMN notification_receipts jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_notification_receipts_chk
 CHECK (jsonb_typeof(notification_receipts)='object' AND notification_receipts-ARRAY['event_recovery.completed','event_recovery.cancelled','event_recovery.expired','event_recovery.execution_finished']::text[]='{}'::jsonb);

-- +goose Down
ALTER TABLE event_recovery_jobs DROP COLUMN notification_receipts;
