-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS history_pruned_events bigint NOT NULL DEFAULT 0 CHECK (history_pruned_events>=0);
CREATE TABLE IF NOT EXISTS managed_realtime_group_delivery_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id>0),
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 sequence bigint NOT NULL CHECK (sequence>0),
 event text NOT NULL CHECK (event IN ('claimed','retried','lease_expired','dead_lettered','deadline_expired','invalid_deadline','completed','replayed')),
 attempt integer NOT NULL CHECK (attempt BETWEEN 0 AND 10),
 fencing_token bigint CHECK (fencing_token IS NULL OR fencing_token>0),
 failure_code text NOT NULL CHECK (octet_length(failure_code)<=64 AND failure_code~'^[ -~]*$'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(recorded_at))
);
CREATE INDEX IF NOT EXISTS realtime_delivery_history_group_order ON managed_realtime_group_delivery_history(group_id,id);
CREATE INDEX IF NOT EXISTS realtime_delivery_history_message_order ON managed_realtime_group_delivery_history(group_id,sequence,id);
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('record_realtime_delivery_history(uuid,bigint,text,integer,bigint,text)') IS NULL THEN
CREATE FUNCTION record_realtime_delivery_history(gid uuid,seq bigint,kind text,attempt_count integer,fence bigint,code text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE removed bigint; expired bigint;
BEGIN
 -- Existing writers already hold this group lock; also enforce it for direct observers.
 PERFORM id FROM managed_realtime_consumer_groups WHERE id=gid FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 DELETE FROM managed_realtime_group_delivery_history WHERE group_id=gid AND recorded_at<clock_timestamp()-interval '24 hours';
 GET DIAGNOSTICS expired=ROW_COUNT;
 DELETE FROM managed_realtime_group_delivery_history WHERE id IN (
  SELECT id FROM managed_realtime_group_delivery_history WHERE group_id=gid ORDER BY id DESC OFFSET 1023);
 GET DIAGNOSTICS removed=ROW_COUNT;
 IF removed+expired>0 THEN UPDATE managed_realtime_consumer_groups SET history_pruned_events=history_pruned_events+removed+expired WHERE id=gid; END IF;
 INSERT INTO managed_realtime_group_delivery_history(group_id,sequence,event,attempt,fencing_token,failure_code)
 VALUES(gid,seq,kind,attempt_count,fence,code);
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('observe_realtime_delivery_history()') IS NULL THEN
CREATE FUNCTION observe_realtime_delivery_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE lease_expired boolean;
BEGIN
 IF NEW.status='leased' AND OLD.status='ready' THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'claimed',NEW.attempts,NEW.fencing_token,'');
 ELSIF OLD.status='dead' AND NEW.status='ready' THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'replayed',NEW.attempts,NEW.fencing_token,'');
 ELSIF NEW.status='dead' AND OLD.status<>'dead' THEN
  IF NEW.deadline_invalid THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'invalid_deadline',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  ELSIF NEW.delivery_deadline IS NOT NULL AND NEW.delivery_deadline<=clock_timestamp() THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'deadline_expired',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  ELSIF OLD.status='leased' AND OLD.lease_until<=clock_timestamp() AND NEW.failure_code='lease_expired' THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'lease_expired',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  END IF;
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'dead_lettered',NEW.attempts,NEW.fencing_token,NEW.failure_code);
 ELSIF OLD.status='leased' AND NEW.status='ready' THEN
  lease_expired:=OLD.lease_until<=clock_timestamp() AND NEW.failure_code='lease_expired';
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,CASE WHEN lease_expired THEN 'lease_expired' ELSE 'retried' END,NEW.attempts,NEW.fencing_token,NEW.failure_code);
 END IF;
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_group_messages') AND tgname='realtime_delivery_history_transition') THEN
CREATE TRIGGER realtime_delivery_history_transition AFTER UPDATE ON managed_realtime_consumer_group_messages FOR EACH ROW EXECUTE FUNCTION observe_realtime_delivery_history();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_delivery_history_transition ON managed_realtime_consumer_group_messages;
DROP FUNCTION observe_realtime_delivery_history();
DROP FUNCTION record_realtime_delivery_history(uuid,bigint,text,integer,bigint,text);
DROP TABLE managed_realtime_group_delivery_history;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN history_pruned_events;
