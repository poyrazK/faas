-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS draining boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_groups') AND conname='realtime_consumer_group_drain_paused') THEN
ALTER TABLE managed_realtime_consumer_groups ADD CONSTRAINT realtime_consumer_group_drain_paused CHECK (NOT draining OR paused_at IS NOT NULL);
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_permissions_check' AND position('''drain''' in pg_get_constraintdef(oid))>0) OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_last_operation_check' AND position('''drain''' in pg_get_constraintdef(oid))>0) THEN
ALTER TABLE managed_realtime_worker_credentials
 DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_permissions_check,
 DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK
 (cardinality(permissions) BETWEEN 1 AND 12 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK
 (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
-- Incompatible drain grants/usage block downgrade instead of being removed.
ALTER TABLE managed_realtime_worker_credentials
 DROP CONSTRAINT managed_realtime_worker_credentials_permissions_check,
 DROP CONSTRAINT managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK
 (cardinality(permissions) BETWEEN 1 AND 11 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK
 (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume'));
ALTER TABLE managed_realtime_consumer_groups DROP CONSTRAINT realtime_consumer_group_drain_paused, DROP COLUMN draining;
-- Paused state survives downgrade; resume remains explicit.
