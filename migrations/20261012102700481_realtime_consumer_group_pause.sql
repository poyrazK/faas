-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS paused_at timestamptz;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_permissions_check' AND position('''pause''' in pg_get_constraintdef(oid))>0) OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_last_operation_check' AND position('''pause''' in pg_get_constraintdef(oid))>0) THEN
ALTER TABLE managed_realtime_worker_credentials
 DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_permissions_check,
 DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK
 (cardinality(permissions) BETWEEN 1 AND 11 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK
 (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
-- Refuse downgrade while credentials still use new grants or usage vocabulary.
ALTER TABLE managed_realtime_worker_credentials
 DROP CONSTRAINT managed_realtime_worker_credentials_permissions_check,
 DROP CONSTRAINT managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK
 (cardinality(permissions) BETWEEN 1 AND 9 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK
 (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete'));
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN paused_at;
