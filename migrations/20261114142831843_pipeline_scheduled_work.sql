-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS work_config jsonb NOT NULL DEFAULT '{}';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS work_status jsonb NOT NULL DEFAULT '{}';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_work_config_check') THEN
 ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_work_config_check CHECK(jsonb_typeof(work_config)='object' AND octet_length(work_config::text)<=2097152 AND (work_config='{}' OR work_config->>'version'='1')) NOT VALID;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_work_status_check') THEN
 ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_work_status_check CHECK(jsonb_typeof(work_status)='object' AND octet_length(work_status::text)<=131072) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_work_config_check;
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_work_status_check;

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
  IF evolution AND coalesce(protocol,'') NOT IN ('pipeline-evolution-v1','pipeline-scheduled-v1') THEN RETURN NULL; END IF;
  IF operability AND (protocol IS NULL OR protocol NOT IN ('pipeline-operability-v1','pipeline-evolution-v1','pipeline-scheduled-v1')) THEN RETURN NULL; END IF;
  IF serving AND (protocol IS NULL OR protocol NOT IN ('customer-serving-v1','pipeline-operability-v1','pipeline-evolution-v1','pipeline-scheduled-v1')) THEN RETURN NULL; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_scheduled_worker_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='data_pipelines' THEN
  IF TG_OP='UPDATE' AND (NEW.work_config ? 'snapshot') IS DISTINCT FROM (OLD.work_config ? 'snapshot') THEN
   RAISE EXCEPTION 'pipeline import mode is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.work_config<>'{}' AND NEW.lease_token<>'' AND (TG_OP='INSERT' OR NEW.lease_token IS DISTINCT FROM OLD.lease_token)
   AND current_setting('gregale.pipeline_worker_protocol',true) IS DISTINCT FROM 'pipeline-scheduled-v1' THEN RETURN NULL; END IF;
 ELSE
  IF NEW.result->>'mode'='snapshot' AND NEW.lease_token<>'' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token
   AND current_setting('gregale.pipeline_worker_protocol',true) IS DISTINCT FROM 'pipeline-scheduled-v1' THEN RETURN NULL; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS data_pipelines_scheduled_fence ON data_pipelines;
CREATE TRIGGER data_pipelines_scheduled_fence BEFORE UPDATE OF work_config,lease_token ON data_pipelines FOR EACH ROW EXECUTE FUNCTION pipeline_scheduled_worker_fence();
DROP TRIGGER IF EXISTS data_pipeline_checks_scheduled_fence ON data_connection_checks;
CREATE TRIGGER data_pipeline_checks_scheduled_fence BEFORE UPDATE OF lease_token ON data_connection_checks FOR EACH ROW EXECUTE FUNCTION pipeline_scheduled_worker_fence();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION postgres_capacity_pipeline_reserve(pid uuid, acct uuid, src uuid, dst uuid, scheduled boolean)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE s postgres_capacity_sources; t postgres_capacity_targets;
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 SELECT m.* INTO s FROM postgres_capacity_sources m JOIN data_pipeline_connections c ON c.id=m.connection_id AND c.account_id=m.account_id
 WHERE m.connection_id=src AND m.account_id=acct AND m.connection_revision=c.revision;
 IF NOT FOUND THEN RAISE EXCEPTION 'source capacity requires qualification' USING ERRCODE='PCC02'; END IF;
 SELECT m.* INTO t FROM postgres_capacity_targets m JOIN managed_postgres_databases d ON d.id=m.database_id AND d.account_id=m.account_id
 WHERE m.database_id=dst AND m.account_id=acct AND m.database_generation=d.observed_generation AND d.desired_generation=d.observed_generation
  AND d.state='ready' AND m.backend_fingerprint=d.backend_fingerprint AND m.data_resource_id=d.data_resource_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'mirror capacity requires qualification' USING ERRCODE='PCC02'; END IF;
 PERFORM postgres_capacity_reserve('pipeline',pid,'source',s.resource_key,s.placement,s.generation,CASE WHEN scheduled THEN 2 ELSE 5 END,CASE WHEN scheduled THEN 0 ELSE 3 END,CASE WHEN scheduled THEN 0 ELSE 3 END,0);
 PERFORM postgres_capacity_reserve('pipeline',pid,'target',t.resource_key,t.placement,t.generation,CASE WHEN scheduled THEN 2 ELSE 5 END,0,0,CASE WHEN scheduled THEN 0 ELSE 3 END);
END; $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION postgres_capacity_pipeline_reserve(pid uuid, acct uuid, src uuid, dst uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM postgres_capacity_pipeline_reserve(pid,acct,src,dst,coalesce((SELECT work_config ? 'snapshot' FROM data_pipelines WHERE id=pid),false));
END; $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION postgres_capacity_pipeline_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM postgres_capacity_pipeline_reserve(NEW.id,NEW.account_id,NEW.source_connection_id,NEW.database_id,NEW.work_config ? 'snapshot');
 ELSIF NEW.state='deleted' AND OLD.state<>'deleted' THEN
  PERFORM pg_advisory_xact_lock(51733);
  DELETE FROM postgres_capacity_reservations WHERE owner_kind='pipeline' AND owner_id=NEW.id;
 ELSIF (NEW.source_connection_id,NEW.database_id) IS DISTINCT FROM (OLD.source_connection_id,OLD.database_id) THEN
  RAISE EXCEPTION 'reserved pipeline placement is immutable' USING ERRCODE='PCC02';
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pipeline_freshness_alert_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.enabled AND NEW.revision=OLD.revision
 AND coalesce(current_setting('gregale.pipeline_worker_protocol',true),'') NOT IN ('pipeline-operability-v1','pipeline-evolution-v1','pipeline-scheduled-v1')
 AND EXISTS(SELECT 1 FROM data_pipelines WHERE id=NEW.pipeline_id AND freshness_target_seconds>0) THEN
  RAISE EXCEPTION 'freshness-aware monitor required' USING ERRCODE='55000';
 END IF;
 IF NEW.enabled AND NEW.revision=OLD.revision
 AND current_setting('gregale.pipeline_worker_protocol',true) IS DISTINCT FROM 'pipeline-scheduled-v1'
 AND EXISTS(SELECT 1 FROM data_pipelines WHERE id=NEW.pipeline_id AND work_config<>'{}') THEN
  RAISE EXCEPTION 'scheduled-work-aware monitor required' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_alerts'::regclass AND conname='data_pipeline_alerts_candidate_code_v3_check') THEN
 ALTER TABLE data_pipeline_alerts ADD CONSTRAINT data_pipeline_alerts_candidate_code_v3_check CHECK(candidate_code IN ('','recovered','monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','freshness_unconfirmed','quality_failed','snapshot_stale')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_alerts VALIDATE CONSTRAINT data_pipeline_alerts_candidate_code_v3_check;
ALTER TABLE data_pipeline_alerts DROP CONSTRAINT IF EXISTS data_pipeline_alerts_candidate_code_v2_check;

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_alerts'::regclass AND conname='data_pipeline_alerts_incident_code_v3_check') THEN
 ALTER TABLE data_pipeline_alerts ADD CONSTRAINT data_pipeline_alerts_incident_code_v3_check CHECK(incident_code IN ('','monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','freshness_unconfirmed','quality_failed','snapshot_stale')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_alerts VALIDATE CONSTRAINT data_pipeline_alerts_incident_code_v3_check;
ALTER TABLE data_pipeline_alerts DROP CONSTRAINT IF EXISTS data_pipeline_alerts_incident_code_v2_check;

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipeline_alert_deliveries'::regclass AND conname='data_pipeline_alert_deliveries_code_v3_check') THEN
 ALTER TABLE data_pipeline_alert_deliveries ADD CONSTRAINT data_pipeline_alert_deliveries_code_v3_check CHECK(code IN ('monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','freshness_unconfirmed','quality_failed','snapshot_stale')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE data_pipeline_alert_deliveries VALIDATE CONSTRAINT data_pipeline_alert_deliveries_code_v3_check;
ALTER TABLE data_pipeline_alert_deliveries DROP CONSTRAINT IF EXISTS data_pipeline_alert_deliveries_code_v2_check;
RESET lock_timeout;

-- +goose Down
-- Preserve scheduled-mode claims and mode immutability during rollback.
SELECT 1;
