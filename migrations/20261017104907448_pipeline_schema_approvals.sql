-- +goose Up
ALTER TABLE data_pipelines ADD COLUMN schema_approval text NOT NULL DEFAULT '' CHECK(schema_approval='' OR schema_approval ~ '^[a-f0-9]{64}$');
ALTER TABLE data_pipelines ADD COLUMN schema_change jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(schema_change)='object' AND octet_length(schema_change::text)<=4194304);
CREATE TABLE data_pipeline_schema_refreshes (
 pipeline_id uuid NOT NULL REFERENCES data_pipelines(id) ON DELETE CASCADE,
 change_id text NOT NULL CHECK(change_id ~ '^[a-f0-9]{64}$'),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 attempt integer NOT NULL CHECK(attempt BETWEEN 0 AND 10),
 wake_id uuid NOT NULL UNIQUE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(pipeline_id,change_id,attempt,app_id)
);
-- +goose Down
DROP TABLE data_pipeline_schema_refreshes;
ALTER TABLE data_pipelines DROP COLUMN schema_change,DROP COLUMN schema_approval;
