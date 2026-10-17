-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS reader_change jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(reader_change)='object' AND octet_length(reader_change::text)<=131072 AND (reader_change='{}' OR reader_change->>'State' IN ('preview','approved','applied')));
-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines DROP COLUMN IF EXISTS reader_change;
