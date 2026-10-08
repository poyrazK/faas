-- +goose Up
-- No execution FK: job metadata must survive invocation retention.
ALTER TABLE event_recovery_items
 ADD COLUMN replay_invocation_id uuid,
 ADD COLUMN replay_generation bigint,
 ADD COLUMN replay_created_at timestamptz,
 ADD CONSTRAINT event_recovery_items_replay_identity_chk CHECK (
  (replay_invocation_id IS NULL AND replay_generation IS NULL AND replay_created_at IS NULL)
  OR (replay_invocation_id IS NOT NULL AND replay_generation IS NOT NULL AND replay_generation>=0 AND replay_created_at IS NOT NULL AND state='queued')
 );

-- +goose Down
ALTER TABLE event_recovery_items
 DROP CONSTRAINT event_recovery_items_replay_identity_chk,
 DROP COLUMN replay_invocation_id,
 DROP COLUMN replay_generation,
 DROP COLUMN replay_created_at;
