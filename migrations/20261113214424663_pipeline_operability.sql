-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS freshness_target_seconds bigint NOT NULL DEFAULT 0;
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS index_change jsonb NOT NULL DEFAULT '{}';
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_freshness_target_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_freshness_target_check
   CHECK(freshness_target_seconds=0 OR freshness_target_seconds BETWEEN 15 AND 86400) NOT VALID;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_index_change_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_index_change_check
   CHECK(jsonb_typeof(index_change)='object' AND octet_length(index_change::text)<=65536
   AND (index_change='{}' OR coalesce(index_change->>'state','') IN ('requested','preview','approved','applied','blocked','cancelled'))) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_freshness_target_check;
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_index_change_check;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_serving_worker_claim() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE serving boolean; operability boolean; protocol text;
BEGIN
 protocol := current_setting('gregale.pipeline_worker_protocol',true);
 IF TG_TABLE_NAME='data_pipelines' THEN
  serving := NEW.serving_config<>'{}'::jsonb;
  operability := NEW.index_change->>'state'='approved';
 ELSE
  serving := coalesce(NEW.request->'Serving','null'::jsonb)<>'null'::jsonb;
  operability := coalesce((NEW.request->>'FreshnessTargetSeconds')::bigint,0)>0;
 END IF;
 IF NEW.lease_token<>'' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token THEN
  IF operability AND protocol IS DISTINCT FROM 'pipeline-operability-v1' THEN RETURN NULL; END IF;
  IF serving AND (protocol IS NULL OR protocol NOT IN ('customer-serving-v1','pipeline-operability-v1')) THEN RETURN NULL; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_alerts'::regclass AND conname='data_pipeline_alerts_candidate_code_v2_check') THEN
 ALTER TABLE data_pipeline_alerts ADD CONSTRAINT data_pipeline_alerts_candidate_code_v2_check CHECK(candidate_code IN ('','monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','freshness_unconfirmed','recovered')) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_alerts VALIDATE CONSTRAINT data_pipeline_alerts_candidate_code_v2_check;
ALTER TABLE data_pipeline_alerts DROP CONSTRAINT IF EXISTS data_pipeline_alerts_candidate_code_check;
-- +goose StatementBegin
DO $migration$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_alerts'::regclass AND conname='data_pipeline_alerts_incident_code_v2_check') THEN
 ALTER TABLE data_pipeline_alerts ADD CONSTRAINT data_pipeline_alerts_incident_code_v2_check CHECK(incident_code IN ('','monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','freshness_unconfirmed')) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_alerts VALIDATE CONSTRAINT data_pipeline_alerts_incident_code_v2_check;
ALTER TABLE data_pipeline_alerts DROP CONSTRAINT IF EXISTS data_pipeline_alerts_incident_code_check;
-- +goose StatementBegin
DO $migration$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_alert_deliveries'::regclass AND conname='data_pipeline_alert_deliveries_code_v2_check') THEN
 ALTER TABLE data_pipeline_alert_deliveries ADD CONSTRAINT data_pipeline_alert_deliveries_code_v2_check CHECK(code IN ('monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','freshness_unconfirmed')) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_alert_deliveries VALIDATE CONSTRAINT data_pipeline_alert_deliveries_code_v2_check;
ALTER TABLE data_pipeline_alert_deliveries DROP CONSTRAINT IF EXISTS data_pipeline_alert_deliveries_code_check;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_freshness_alert_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.enabled AND NEW.revision=OLD.revision AND current_setting('gregale.pipeline_worker_protocol',true) IS DISTINCT FROM 'pipeline-operability-v1'
 AND EXISTS(SELECT 1 FROM data_pipelines WHERE id=NEW.pipeline_id AND freshness_target_seconds>0) THEN
  RAISE EXCEPTION 'freshness-aware monitor required' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS pipeline_freshness_alert_fence ON data_pipeline_alerts;
CREATE TRIGGER pipeline_freshness_alert_fence BEFORE UPDATE ON data_pipeline_alerts FOR EACH ROW EXECUTE FUNCTION pipeline_freshness_alert_fence();
RESET lock_timeout;
-- +goose Down
-- Keep the claim fence and persisted contracts when rolling binaries back.
SELECT 1;
