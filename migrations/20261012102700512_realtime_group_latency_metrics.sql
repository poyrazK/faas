-- +goose Up
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS processing_started_at timestamptz CHECK (processing_started_at IS NULL OR isfinite(processing_started_at));
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS first_claim_observed boolean NOT NULL DEFAULT false CHECK (first_claim_observed IN (true,false));
-- Preserve first-claim identity across replay of pre-migration attempted rows.
UPDATE managed_realtime_consumer_group_messages SET first_claim_observed=true WHERE attempts>0 AND NOT first_claim_observed;
CREATE TABLE IF NOT EXISTS managed_realtime_group_metrics (
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 bucket_at timestamptz NOT NULL CHECK (isfinite(bucket_at) AND bucket_at=date_trunc('minute',bucket_at,'UTC')),
 first_claims bigint NOT NULL DEFAULT 0 CHECK (first_claims>=0),
 completed bigint NOT NULL DEFAULT 0 CHECK (completed>=0),
 retries bigint NOT NULL DEFAULT 0 CHECK (retries>=0),
 dead_letters bigint NOT NULL DEFAULT 0 CHECK (dead_letters>=0),
 queue_milliseconds numeric NOT NULL DEFAULT 0 CHECK (queue_milliseconds>=0),
 processing_milliseconds numeric NOT NULL DEFAULT 0 CHECK (processing_milliseconds>=0),
 PRIMARY KEY(group_id,bucket_at)
);
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('record_realtime_group_metric(uuid,bigint,bigint,bigint,bigint,numeric,numeric)') IS NULL THEN
CREATE FUNCTION record_realtime_group_metric(g uuid, first_count bigint, complete_count bigint, retry_count bigint, dead_count bigint, queue_ms numeric, processing_ms numeric) RETURNS void LANGUAGE plpgsql AS $$
DECLARE bucket timestamptz := date_trunc('minute',clock_timestamp(),'UTC');
BEGIN
 DELETE FROM managed_realtime_group_metrics WHERE group_id=g AND (bucket_at<bucket-interval '59 minutes' OR bucket_at>bucket);
 INSERT INTO managed_realtime_group_metrics(group_id,bucket_at,first_claims,completed,retries,dead_letters,queue_milliseconds,processing_milliseconds)
 VALUES(g,bucket,first_count,complete_count,retry_count,dead_count,queue_ms,processing_ms)
 ON CONFLICT(group_id,bucket_at) DO UPDATE SET
 first_claims=managed_realtime_group_metrics.first_claims+excluded.first_claims,
 completed=managed_realtime_group_metrics.completed+excluded.completed,
 retries=managed_realtime_group_metrics.retries+excluded.retries,
 dead_letters=managed_realtime_group_metrics.dead_letters+excluded.dead_letters,
 queue_milliseconds=managed_realtime_group_metrics.queue_milliseconds+excluded.queue_milliseconds,
 processing_milliseconds=managed_realtime_group_metrics.processing_milliseconds+excluded.processing_milliseconds;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('measure_realtime_group_transition()') IS NULL THEN
CREATE FUNCTION measure_realtime_group_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wait_ms numeric := 0; first_count bigint := 0; retry_count bigint := 0; dead_count bigint := 0;
BEGIN
 IF NEW.status='leased' AND OLD.status<>'leased' THEN
  NEW.processing_started_at:=clock_timestamp();
  IF NOT OLD.first_claim_observed THEN
   NEW.first_claim_observed:=true;
   -- Pre-migration attempted rows have no first-claim timing baseline.
   IF OLD.attempts=0 THEN
    first_count:=1;
    wait_ms:=greatest(0,extract(epoch FROM (NEW.processing_started_at-(NEW.message->>'created_at')::timestamptz))*1000);
   END IF;
  END IF;
 END IF;
 IF OLD.status='leased' AND NEW.status='ready' THEN retry_count:=1; END IF;
 IF OLD.status<>'dead' AND NEW.status='dead' THEN dead_count:=1; END IF;
 IF NEW.status<>'leased' THEN NEW.processing_started_at:=NULL; END IF;
 IF first_count+retry_count+dead_count>0 THEN
  PERFORM record_realtime_group_metric(NEW.group_id,first_count,0,retry_count,dead_count,wait_ms,0);
 END IF;
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_group_messages') AND tgname='realtime_group_measure_transition') THEN
CREATE TRIGGER realtime_group_measure_transition BEFORE UPDATE ON managed_realtime_consumer_group_messages FOR EACH ROW EXECUTE FUNCTION measure_realtime_group_transition();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_group_measure_transition ON managed_realtime_consumer_group_messages;
DROP FUNCTION measure_realtime_group_transition();
DROP FUNCTION record_realtime_group_metric(uuid,bigint,bigint,bigint,bigint,numeric,numeric);
DROP TABLE managed_realtime_group_metrics;
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN processing_started_at,DROP COLUMN first_claim_observed;
