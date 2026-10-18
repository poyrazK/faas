-- ADR-1174: Live per-subscription ceilings share the existing capacity mutex.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_subscription_delivery_controls ADD COLUMN IF NOT EXISTS max_pending_deliveries integer NOT NULL DEFAULT 0 CHECK (max_pending_deliveries >= 0);
CREATE OR REPLACE FUNCTION guard_event_delivery_replay() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE slot event_delivery_slots%ROWTYPE; caps event_delivery_capacity%ROWTYPE;
 consumer_count bigint; app_count bigint; account_count bigint; scope text; consumer_limit integer;
BEGIN
 IF NEW.state NOT IN ('pending','dispatching') THEN RETURN NEW; END IF;
 IF TG_OP = 'UPDATE' AND OLD.state IN ('pending','dispatching') THEN RETURN NEW; END IF;
 SELECT * INTO slot FROM event_delivery_slots WHERE invocation_id=NEW.id;
 IF NOT FOUND AND NEW.replayed_from_invocation_id IS NOT NULL THEN
  SELECT * INTO slot FROM event_delivery_slots WHERE invocation_id=NEW.replayed_from_invocation_id;
 END IF;
 IF slot.invocation_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO STRICT caps FROM event_delivery_capacity WHERE account_id=slot.account_id FOR UPDATE;
 SELECT count(*), count(*) FILTER (WHERE s.app_id=slot.app_id),
   count(*) FILTER (WHERE s.app_id=slot.app_id AND s.subscription_id=slot.subscription_id)
 INTO account_count, app_count, consumer_count
 FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
 WHERE s.account_id=slot.account_id AND i.state IN ('pending','dispatching') AND i.id<>NEW.id;
 SELECT CASE WHEN c.max_pending_deliveries>0 THEN least(c.max_pending_deliveries,caps.consumer_limit) ELSE caps.consumer_limit END INTO consumer_limit
 FROM event_subscription_delivery_controls c WHERE c.account_id=slot.account_id AND c.app_id=slot.app_id AND c.subscription_id::text=slot.subscription_id;
 consumer_limit := coalesce(consumer_limit,caps.consumer_limit);
 scope := CASE WHEN consumer_count>=consumer_limit THEN 'consumer'
   WHEN app_count>=caps.app_limit THEN 'app' WHEN account_count>=caps.account_limit THEN 'account' END;
 IF scope IS NOT NULL THEN
  RAISE EXCEPTION 'event delivery capacity exhausted: %',scope USING ERRCODE='23514', CONSTRAINT='event_delivery_capacity', DETAIL=scope;
 END IF;
 INSERT INTO event_delivery_slots VALUES (NEW.id,slot.account_id,slot.app_id,slot.subscription_id) ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_event_delivery_replay() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE slot event_delivery_slots%ROWTYPE; caps event_delivery_capacity%ROWTYPE;
 consumer_count bigint; app_count bigint; account_count bigint; scope text;
BEGIN
 IF NEW.state NOT IN ('pending','dispatching') THEN RETURN NEW; END IF;
 IF TG_OP = 'UPDATE' AND OLD.state IN ('pending','dispatching') THEN RETURN NEW; END IF;
 SELECT * INTO slot FROM event_delivery_slots WHERE invocation_id=NEW.id;
 IF NOT FOUND AND NEW.replayed_from_invocation_id IS NOT NULL THEN
  SELECT * INTO slot FROM event_delivery_slots WHERE invocation_id=NEW.replayed_from_invocation_id;
 END IF;
 IF slot.invocation_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO STRICT caps FROM event_delivery_capacity WHERE account_id=slot.account_id FOR UPDATE;
 SELECT count(*), count(*) FILTER (WHERE s.app_id=slot.app_id),
   count(*) FILTER (WHERE s.app_id=slot.app_id AND s.subscription_id=slot.subscription_id)
 INTO account_count, app_count, consumer_count
 FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
 WHERE s.account_id=slot.account_id AND i.state IN ('pending','dispatching') AND i.id<>NEW.id;
 scope := CASE WHEN consumer_count>=caps.consumer_limit THEN 'consumer'
   WHEN app_count>=caps.app_limit THEN 'app' WHEN account_count>=caps.account_limit THEN 'account' END;
 IF scope IS NOT NULL THEN
  RAISE EXCEPTION 'event delivery capacity exhausted: %',scope USING ERRCODE='23514', CONSTRAINT='event_delivery_capacity', DETAIL=scope;
 END IF;
 INSERT INTO event_delivery_slots VALUES (NEW.id,slot.account_id,slot.app_id,slot.subscription_id) ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
ALTER TABLE event_subscription_delivery_controls DROP COLUMN max_pending_deliveries;
-- +goose StatementEnd
