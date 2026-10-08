-- +goose Up
CREATE TABLE managed_realtime_notification_timeline (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL,
 message_id text NOT NULL,
 device text NOT NULL DEFAULT '',
 delivery_id text NOT NULL DEFAULT '',
 event text NOT NULL,
 reason text NOT NULL DEFAULT '',
 attempts integer NOT NULL DEFAULT 0,
 status_code integer NOT NULL DEFAULT 0,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 not_before timestamptz NOT NULL DEFAULT 'epoch',
 next_attempt timestamptz NOT NULL DEFAULT 'epoch'
);
CREATE INDEX managed_realtime_notification_timeline_lookup_idx ON managed_realtime_notification_timeline(endpoint_id,principal,message_id,id DESC);
CREATE INDEX managed_realtime_notification_timeline_endpoint_idx ON managed_realtime_notification_timeline(endpoint_id,id DESC);
CREATE INDEX managed_realtime_notification_timeline_cleanup_idx ON managed_realtime_notification_timeline(occurred_at);
-- +goose StatementBegin
CREATE FUNCTION faas_record_notification_timeline() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE eid uuid; pk text; mid text; event_name text;
BEGIN
 eid:=CASE WHEN TG_OP='DELETE' THEN OLD.endpoint_id ELSE NEW.endpoint_id END;
 IF NOT EXISTS(SELECT 1 FROM managed_realtime_endpoints WHERE id=eid) THEN
   IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 IF TG_TABLE_NAME='managed_realtime_push_deliveries' THEN
   IF TG_OP='UPDATE' AND ROW(OLD.status,OLD.code,OLD.attempts,OLD.status_code,OLD.not_before,OLD.next_attempt)
       IS NOT DISTINCT FROM ROW(NEW.status,NEW.code,NEW.attempts,NEW.status_code,NEW.not_before,NEW.next_attempt) THEN RETURN NEW; END IF;
   eid:=NEW.endpoint_id; pk:=NEW.principal; mid:=NEW.message_id;
   INSERT INTO managed_realtime_notification_timeline(endpoint_id,principal,message_id,device,delivery_id,event,reason,attempts,status_code,not_before,next_attempt)
    VALUES(eid,pk,mid,NEW.device,NEW.id::text,NEW.status,NEW.code,NEW.attempts,NEW.status_code,NEW.not_before,NEW.next_attempt);
 ELSE
   IF TG_OP='DELETE' THEN
    eid:=OLD.endpoint_id; pk:=OLD.principal; mid:=OLD.message_id;event_name:='fallback_removed';
    INSERT INTO managed_realtime_notification_timeline(endpoint_id,principal,message_id,event,not_before,next_attempt) VALUES(eid,pk,mid,event_name,OLD.not_before,OLD.deadline);
   ELSE
    IF TG_OP='UPDATE' AND OLD.not_before IS NOT DISTINCT FROM NEW.not_before THEN RETURN NEW; END IF;
    eid:=NEW.endpoint_id;pk:=NEW.principal;mid:=NEW.message_id;
    event_name:=CASE WHEN TG_OP='INSERT' THEN 'fallback_scheduled' ELSE 'fallback_rescheduled' END;
    INSERT INTO managed_realtime_notification_timeline(endpoint_id,principal,message_id,event,not_before,next_attempt) VALUES(eid,pk,mid,event_name,NEW.not_before,NEW.deadline);
   END IF;
 END IF;
 -- Bounded diagnostic history; no payloads or credential columns are copied.
 DELETE FROM managed_realtime_notification_timeline WHERE endpoint_id=eid AND (occurred_at<clock_timestamp()-interval '7 days' OR id<=coalesce((SELECT id FROM managed_realtime_notification_timeline WHERE endpoint_id=eid ORDER BY id DESC OFFSET 8192 LIMIT 1),0));
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER managed_realtime_push_timeline AFTER INSERT OR UPDATE ON managed_realtime_push_deliveries FOR EACH ROW EXECUTE FUNCTION faas_record_notification_timeline();
CREATE TRIGGER managed_realtime_fallback_timeline AFTER INSERT OR UPDATE OR DELETE ON managed_realtime_inbox_fallbacks FOR EACH ROW EXECUTE FUNCTION faas_record_notification_timeline();
-- +goose Down
DROP TRIGGER managed_realtime_fallback_timeline ON managed_realtime_inbox_fallbacks;
DROP TRIGGER managed_realtime_push_timeline ON managed_realtime_push_deliveries;
DROP FUNCTION faas_record_notification_timeline();
DROP TABLE managed_realtime_notification_timeline;
