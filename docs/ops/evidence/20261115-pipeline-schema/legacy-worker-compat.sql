\set ON_ERROR_STOP on
BEGIN;
DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN CREATE ROLE faas_runtime NOLOGIN; END IF; END $$;
GRANT USAGE ON SCHEMA public TO faas_runtime;
CREATE TEMP TABLE data_pipelines AS SELECT * FROM public.data_pipelines WITH NO DATA;
CREATE TEMP TABLE data_pipeline_alerts AS SELECT * FROM public.data_pipeline_alerts WITH NO DATA;
CREATE TRIGGER compat_claim BEFORE UPDATE OF lease_token ON data_pipelines
FOR EACH ROW EXECUTE FUNCTION public.pipeline_serving_worker_claim();
CREATE TRIGGER compat_scheduled BEFORE UPDATE OF work_config,lease_token,generation,verify_requested,schema_change,tables,serving_config ON data_pipelines
FOR EACH ROW EXECUTE FUNCTION public.pipeline_scheduled_worker_fence();
CREATE TRIGGER compat_alert BEFORE UPDATE ON data_pipeline_alerts
FOR EACH ROW EXECUTE FUNCTION public.pipeline_freshness_alert_fence();
INSERT INTO data_pipelines(id,lease_token,serving_config,index_change,work_config,generation,verify_requested,initialized,freshness_target_seconds)
VALUES('00000000-0000-4000-8000-000000000001','initial','{}','{}','{}',1,false,false,15);
INSERT INTO data_pipeline_alerts(pipeline_id,enabled,revision)
VALUES('00000000-0000-4000-8000-000000000001',true,1);
CREATE FUNCTION pg_temp.assert_claim(protocol text,next_token text,wanted integer) RETURNS void LANGUAGE plpgsql AS $$
DECLARE changed integer;
BEGIN
 PERFORM set_config('gregale.pipeline_worker_protocol',protocol,true);
 UPDATE pg_temp.data_pipelines SET lease_token=next_token;
 GET DIAGNOSTICS changed=ROW_COUNT;
 IF changed<>wanted THEN RAISE EXCEPTION 'protocol % changed % rows, wanted %',protocol,changed,wanted; END IF;
END $$;
DO $$ DECLARE temp_schema name; BEGIN SELECT nspname INTO temp_schema FROM pg_namespace WHERE oid=pg_my_temp_schema(); EXECUTE format('GRANT USAGE ON SCHEMA %I TO faas_runtime',temp_schema); END $$;
GRANT SELECT,INSERT,UPDATE ON data_pipelines,data_pipeline_alerts TO faas_runtime;
GRANT EXECUTE ON FUNCTION pg_temp.assert_claim(text,text,integer) TO faas_runtime;
SET LOCAL ROLE faas_runtime;
SELECT pg_temp.assert_claim('pipeline-operability-v1','legacy-cdc',1);
UPDATE data_pipelines SET serving_config='{"version":1}';
SELECT pg_temp.assert_claim('customer-serving-v1','legacy-serving',1);
UPDATE data_pipelines SET index_change='{"state":"approved"}';
SELECT pg_temp.assert_claim('pipeline-operability-v1','legacy-index',1);
UPDATE data_pipeline_alerts SET enabled=true;
UPDATE data_pipelines SET serving_config='{"version":2}',index_change='{}';
SELECT pg_temp.assert_claim('pipeline-operability-v1','blocked-evolution',0);
SELECT pg_temp.assert_claim('pipeline-evolution-v1','accepted-evolution',1);
UPDATE data_pipelines SET serving_config='{"version":1}',work_config='{"version":1}';
SELECT pg_temp.assert_claim('pipeline-operability-v1','blocked-work-v1',0);
SELECT pg_temp.assert_claim('pipeline-evolution-v1','blocked-work-v2',0);
SELECT pg_temp.assert_claim('pipeline-scheduled-v1','accepted-work-v3',1);
DO $$
DECLARE blocked boolean:=false;
BEGIN
 PERFORM set_config('gregale.pipeline_worker_protocol','pipeline-operability-v1',true);
 BEGIN
  UPDATE pg_temp.data_pipeline_alerts SET enabled=true;
 EXCEPTION WHEN SQLSTATE '55000' THEN blocked:=true;
 END;
 IF NOT blocked THEN RAISE EXCEPTION 'older monitor renewed scheduled-work observation'; END IF;
 PERFORM set_config('gregale.pipeline_worker_protocol','pipeline-scheduled-v1',true);
 UPDATE pg_temp.data_pipeline_alerts SET enabled=true;
END $$;
SELECT 'legacy CDC/serving/index/monitor accepted; advanced evolution/work/monitor fenced; capable protocols accepted' AS result;
ROLLBACK;
