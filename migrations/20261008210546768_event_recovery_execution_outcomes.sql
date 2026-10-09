-- +goose Up
-- +goose StatementBegin
-- No execution FK: job metadata must survive invocation retention.
ALTER TABLE event_recovery_items
    ADD COLUMN IF NOT EXISTS replay_invocation_id uuid,
    ADD COLUMN IF NOT EXISTS replay_generation bigint,
    ADD COLUMN IF NOT EXISTS replay_created_at timestamptz;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_items'::regclass AND conname='event_recovery_items_replay_identity_chk') THEN
        ALTER TABLE event_recovery_items ADD CONSTRAINT event_recovery_items_replay_identity_chk CHECK (
  (replay_invocation_id IS NULL AND replay_generation IS NULL AND replay_created_at IS NULL)
  OR (replay_invocation_id IS NOT NULL AND replay_generation IS NOT NULL AND replay_generation>=0 AND replay_created_at IS NOT NULL AND state='queued')
 );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE event_recovery_items
 DROP CONSTRAINT event_recovery_items_replay_identity_chk,
 DROP COLUMN replay_invocation_id,
 DROP COLUMN replay_generation,
 DROP COLUMN replay_created_at;
