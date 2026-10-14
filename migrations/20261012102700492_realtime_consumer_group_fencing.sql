-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS last_fencing_token bigint NOT NULL DEFAULT 0 CHECK (last_fencing_token>=0);
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS fencing_token bigint CHECK (fencing_token>0);
CREATE UNIQUE INDEX IF NOT EXISTS realtime_group_fencing_unique ON managed_realtime_consumer_group_messages(group_id,fencing_token) WHERE fencing_token IS NOT NULL;
-- Legacy leases deliberately have no retroactively issued token.
-- +goose Down
LOCK TABLE managed_realtime_consumer_groups IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_realtime_consumer_groups WHERE last_fencing_token>0) THEN
  RAISE EXCEPTION 'cannot discard fencing counters for groups that issued tokens; remove those groups or retain the schema';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX realtime_group_fencing_unique;
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN fencing_token;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN last_fencing_token;
-- Downgrade removes fencing support; stop workers before reverting.
