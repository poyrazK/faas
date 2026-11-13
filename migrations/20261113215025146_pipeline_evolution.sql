-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout='5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS evolution_change jsonb NOT NULL DEFAULT '{}';
-- +goose StatementBegin
DO $migration$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_evolution_check') THEN
 ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_evolution_check
 CHECK(jsonb_typeof(evolution_change)='object' AND octet_length(evolution_change::text)<=4194304
 AND (evolution_change='{}' OR coalesce(evolution_change->>'state','') IN ('preview','approved','applied','superseded'))) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_evolution_check;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_serving_worker_claim() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE serving boolean; operability boolean; evolution boolean; protocol text;
BEGIN
 protocol:=current_setting('gregale.pipeline_worker_protocol',true);
 IF TG_TABLE_NAME='data_pipelines' THEN
  serving:=NEW.serving_config<>'{}'::jsonb;
  operability:=NEW.index_change->>'state'='approved';
  evolution:=coalesce(NEW.serving_config->>'version','1')='2';
 ELSE
  serving:=coalesce(NEW.request->'Serving','null'::jsonb)<>'null'::jsonb;
  operability:=coalesce((NEW.request->>'FreshnessTargetSeconds')::bigint,0)>0;
  evolution:=false;
 END IF;
 IF NEW.lease_token<>'' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token THEN
  IF evolution AND protocol IS DISTINCT FROM 'pipeline-evolution-v1' THEN RETURN NULL; END IF;
  IF operability AND (protocol IS NULL OR protocol NOT IN ('pipeline-operability-v1','pipeline-evolution-v1')) THEN RETURN NULL; END IF;
  IF serving AND (protocol IS NULL OR protocol NOT IN ('customer-serving-v1','pipeline-operability-v1','pipeline-evolution-v1')) THEN RETURN NULL; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_freshness_alert_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.enabled AND NEW.revision=OLD.revision
 AND coalesce(current_setting('gregale.pipeline_worker_protocol',true),'') NOT IN ('pipeline-operability-v1','pipeline-evolution-v1')
 AND EXISTS(SELECT 1 FROM data_pipelines WHERE id=NEW.pipeline_id AND freshness_target_seconds>0) THEN
  RAISE EXCEPTION 'freshness-aware monitor required' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
-- +goose Down
-- Persist approval and worker fences across binary rollbacks.
SELECT 1;
