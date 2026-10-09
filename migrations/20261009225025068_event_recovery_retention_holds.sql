-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_jobs'::regclass AND conname='event_recovery_receipt_protection_chk') THEN
  ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_receipt_protection_chk
   CHECK (NOT (selection ? 'protect_receipts') OR jsonb_typeof(selection->'protect_receipts')='boolean');
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS event_fanout_outbox_account_retention_idx ON event_fanout_outbox(account_id,delivered_at,id) WHERE state='delivered';
CREATE INDEX IF NOT EXISTS event_recovery_items_receipt_hold_idx ON event_recovery_items(outbox_id,job_id) WHERE state='pending';
CREATE OR REPLACE FUNCTION event_receipt_retention_hold(p_account uuid, p_outbox bigint, p_accepted timestamptz, p_job_cutoff timestamptz, p_now timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='running'
  AND p_accepted>=j.from_at AND p_accepted<j.cutoff_at) THEN 'backfill_running'
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='completed_with_failures'
  AND j.completed_at>=p_job_cutoff AND EXISTS (SELECT 1 FROM event_replay_job_items i
   WHERE i.job_id=j.id AND i.outbox_id=p_outbox AND i.state='failed' AND i.retryable)) THEN 'backfill_retryable'
 WHEN EXISTS (SELECT 1 FROM event_recovery_items i JOIN event_recovery_jobs j ON j.id=i.job_id
  WHERE i.outbox_id=p_outbox AND i.state='pending' AND j.account_id=p_account
   AND j.selection->'protect_receipts'='true'::jsonb AND j.state IN ('running','paused') AND j.expires_at>p_now) THEN 'recovery_pending'
 ELSE '' END;
$$;
CREATE OR REPLACE FUNCTION event_receipt_retention_hold(p_account uuid, p_outbox bigint, p_accepted timestamptz, p_job_cutoff timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
 SELECT event_receipt_retention_hold(p_account,p_outbox,p_accepted,p_job_cutoff,CURRENT_TIMESTAMP);
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE selection->'protect_receipts'='true'::jsonb AND state IN ('running','paused')) THEN
  RAISE EXCEPTION 'complete or cancel protected recovery jobs before rollback';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION event_receipt_retention_hold(p_account uuid, p_outbox bigint, p_accepted timestamptz, p_job_cutoff timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='running'
  AND p_accepted>=j.from_at AND p_accepted<j.cutoff_at) THEN 'backfill_running'
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='completed_with_failures'
  AND j.completed_at>=p_job_cutoff AND EXISTS (SELECT 1 FROM event_replay_job_items i
   WHERE i.job_id=j.id AND i.outbox_id=p_outbox AND i.state='failed' AND i.retryable)) THEN 'backfill_retryable'
 ELSE '' END;
$$;
DROP FUNCTION IF EXISTS event_receipt_retention_hold(uuid,bigint,timestamptz,timestamptz,timestamptz);
DROP INDEX IF EXISTS event_recovery_items_receipt_hold_idx;
DROP INDEX IF EXISTS event_fanout_outbox_account_retention_idx;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT IF EXISTS event_recovery_receipt_protection_chk;
-- +goose StatementEnd
