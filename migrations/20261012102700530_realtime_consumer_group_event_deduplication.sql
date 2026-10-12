-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS deduplication_metadata_key text NOT NULL DEFAULT '' CHECK (octet_length(deduplication_metadata_key)<=64 AND deduplication_metadata_key~'^[a-z0-9_.-]*$');
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS deduplication_window_seconds integer NOT NULL DEFAULT 0 CHECK (deduplication_window_seconds=0 OR deduplication_window_seconds BETWEEN 60 AND 86400);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS duplicate_messages bigint NOT NULL DEFAULT 0 CHECK (duplicate_messages>=0);
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_groups') AND conname='realtime_group_deduplication_policy_check') THEN
ALTER TABLE managed_realtime_consumer_groups ADD CONSTRAINT realtime_group_deduplication_policy_check CHECK ((deduplication_metadata_key='')=(deduplication_window_seconds=0));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS managed_realtime_group_event_ids (
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 event_hash bytea NOT NULL CHECK (octet_length(event_hash)=32),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
 PRIMARY KEY(group_id,event_hash)
);
CREATE INDEX IF NOT EXISTS realtime_group_event_ids_expiry ON managed_realtime_group_event_ids(group_id,expires_at);
-- +goose Down
DROP TABLE managed_realtime_group_event_ids;
ALTER TABLE managed_realtime_consumer_groups DROP CONSTRAINT realtime_group_deduplication_policy_check,
 DROP COLUMN deduplication_metadata_key,DROP COLUMN deduplication_window_seconds,DROP COLUMN duplicate_messages;
-- Previously advanced cursors and skipped duplicate events cannot be restored on downgrade.
