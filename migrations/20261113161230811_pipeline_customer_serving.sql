-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS serving_config jsonb NOT NULL DEFAULT '{}';
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_serving_config_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_serving_config_check
   CHECK(jsonb_typeof(serving_config)='object' AND octet_length(serving_config::text)<=2097152) NOT VALID;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_read_subjects_v3_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_read_subjects_v3_check
   CHECK(jsonb_typeof(read_subjects)='array' AND jsonb_array_length(read_subjects) BETWEEN 0 AND 128) NOT VALID;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_reader_change_v2_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_reader_change_v2_check
   CHECK(jsonb_typeof(reader_change)='object' AND octet_length(reader_change::text)<=2097152
   AND (reader_change='{}' OR reader_change->>'State' IN ('preview','approved','applied'))) NOT VALID;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_tenant_reader_change_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_tenant_reader_change_check
   CHECK(serving_config->'tenant_access' IS NULL OR reader_change='{}'::jsonb OR coalesce(reader_change->>'Tenant','')='true') NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_serving_config_check;
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_read_subjects_v3_check;
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_reader_change_v2_check;
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_tenant_reader_change_check;
ALTER TABLE data_pipelines DROP CONSTRAINT IF EXISTS data_pipelines_read_subjects_v2_check;
ALTER TABLE data_pipelines DROP CONSTRAINT IF EXISTS data_pipelines_reader_change_check;
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_setups'::regclass AND conname='data_pipeline_setups_request_v2_check') THEN
  ALTER TABLE data_pipeline_setups ADD CONSTRAINT data_pipeline_setups_request_v2_check
   CHECK(jsonb_typeof(request)='object' AND octet_length(request::text)<=2097152) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_setups VALIDATE CONSTRAINT data_pipeline_setups_request_v2_check;
ALTER TABLE data_pipeline_setups DROP CONSTRAINT IF EXISTS data_pipeline_setups_request_check;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_serving_worker_claim() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE serving boolean;
BEGIN
 IF TG_TABLE_NAME='data_pipelines' THEN
  serving := NEW.serving_config<>'{}'::jsonb;
 ELSE
  serving := coalesce(NEW.request->'Serving','null'::jsonb)<>'null'::jsonb;
 END IF;
 IF serving AND NEW.lease_token<>'' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token
 AND current_setting('gregale.pipeline_worker_protocol',true) IS DISTINCT FROM 'customer-serving-v1' THEN
  RETURN NULL;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='data_pipelines'::regclass AND tgname='data_pipelines_serving_claim') THEN
  CREATE TRIGGER data_pipelines_serving_claim BEFORE UPDATE OF lease_token ON data_pipelines
   FOR EACH ROW EXECUTE FUNCTION pipeline_serving_worker_claim();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='data_pipeline_setups'::regclass AND tgname='data_pipeline_setups_serving_claim') THEN
  CREATE TRIGGER data_pipeline_setups_serving_claim BEFORE UPDATE OF lease_token ON data_pipeline_setups
   FOR EACH ROW EXECUTE FUNCTION pipeline_serving_worker_claim();
 END IF;
END $migration$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
-- Forward-only: removing claim fences or shrinking reader limits can weaken an
-- active serving contract. Roll back binaries with the fences and schema kept.
SELECT 1;
