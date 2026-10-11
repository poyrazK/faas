-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN draining boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT realtime_consumer_group_drain_paused CHECK (NOT draining OR paused_at IS NOT NULL);
ALTER TABLE managed_realtime_worker_credentials
 DROP CONSTRAINT managed_realtime_worker_credentials_permissions_check,
 DROP CONSTRAINT managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK
 (cardinality(permissions) BETWEEN 1 AND 12 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK
 (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain'));
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
