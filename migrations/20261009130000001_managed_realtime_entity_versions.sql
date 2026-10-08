-- +goose Up
ALTER TABLE managed_realtime_channel_reducers ADD COLUMN entity_versions json NOT NULL DEFAULT '{}'::json
 CHECK (json_typeof(entity_versions)='object' AND octet_length(entity_versions::text)<=65536);
UPDATE managed_realtime_channel_reducers r SET entity_versions =
 (SELECT COALESCE(json_object_agg(k, 1), '{}'::json) FROM json_object_keys(r.entities) AS keys(k));
-- +goose Down
ALTER TABLE managed_realtime_channel_reducers DROP COLUMN entity_versions;
