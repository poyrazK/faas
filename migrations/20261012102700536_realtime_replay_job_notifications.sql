-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.replay_job.cancelled''' in definition)>0 AND position('''realtime.replay_job.completed''' in definition)>0 AND position('''realtime.replay_job.processing_failed''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L,%L))',substring(definition from 8 for length(definition)-8),'realtime.replay_job.completed','realtime.replay_job.cancelled','realtime.replay_job.processing_failed');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_replay_job_notification()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_replay_job_notification() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; v_endpoint uuid;
 v_channel text; v_group text; v_slug text; event_id uuid; event_name text;
 replayed integer; inspector_path text;
 -- Keep synchronized with api.RealtimeReplayJobNotificationErrorThreshold.
 error_threshold CONSTANT integer := 3;
BEGIN
 IF NEW.state<>OLD.state AND NEW.state IN ('completed','cancelled') THEN
  event_name:='realtime.replay_job.'||NEW.state;
 ELSIF NEW.state='running' AND OLD.error_count<error_threshold AND NEW.error_count>=error_threshold THEN
  event_name:='realtime.replay_job.processing_failed';
 ELSE RETURN NEW;
 END IF;
 SELECT e.app_id,e.account_id,e.id,g.channel,g.name,p.slug,array_agg(h.id ORDER BY h.id)
 INTO v_app,v_account,v_endpoint,v_channel,v_group,v_slug,recipients
 FROM managed_realtime_consumer_groups g
 JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id
 JOIN apps p ON p.id=e.app_id
 JOIN app_webhooks h ON h.app_id=e.app_id AND h.account_id=e.account_id
 WHERE g.id=NEW.group_id AND h.scope='app' AND h.enabled AND event_name=ANY(h.event_filter)
 GROUP BY e.app_id,e.account_id,e.id,g.channel,g.name,p.slug;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 SELECT count(*) INTO replayed FROM jsonb_array_elements(NEW.outcomes) AS outcome WHERE outcome->>'status'='replayed';
 -- Percent-encode every UTF-8 byte in variable URL components, including reserved characters.
 inspector_path:='/dashboard/apps/'||regexp_replace(encode(convert_to(v_slug,'UTF8'),'hex'),'(..)','%\1','g')||'/realtime?endpoint_id='||v_endpoint::text||
  '&channel='||regexp_replace(encode(convert_to(v_channel,'UTF8'),'hex'),'(..)','%\1','g')||
  '&group='||regexp_replace(encode(convert_to(v_group,'UTF8'),'hex'),'(..)','%\1','g')||'#replay-job-'||NEW.id::text;
 event_id:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,event_name,NEW.id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',v_endpoint,'group_id',NEW.group_id,
 'channel',v_channel,'group',v_group,'job_id',NEW.id,'state',NEW.state,'rate',NEW.rate,
 'selected',jsonb_array_length(NEW.selection),'processed',NEW.position,'replayed',replayed,'skipped',NEW.position-replayed,
 'remaining',jsonb_array_length(NEW.selection)-NEW.position,'error_count',NEW.error_count,
 'error_code',CASE WHEN event_name='realtime.replay_job.processing_failed' THEN 'processing_failed' ELSE '' END,
 'error_threshold',error_threshold,'inspector_path',inspector_path,'occurred_at',clock_timestamp()),recipients)
 ON CONFLICT (event,source_id) DO NOTHING;
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_replay_jobs') AND tgname='realtime_replay_job_notification') THEN
CREATE TRIGGER realtime_replay_job_notification AFTER UPDATE ON managed_realtime_replay_jobs
 FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_replay_job_notification();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_replay_job_notification ON managed_realtime_replay_jobs;
DROP FUNCTION enqueue_realtime_replay_job_notification();
-- Retain event vocabulary for queued notifications and delivery history.
