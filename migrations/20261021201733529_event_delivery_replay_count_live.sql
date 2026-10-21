-- migration-compat: expand
-- filename: 20261021201733529_event_delivery_replay_count_live.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- Replay admission counts live deliveries from the account's live
-- invocations, not from its slots. A slot lives as long as its invocation is
-- retained so that replays can inherit it (ADR-614); counting slots made each
-- replay read the account's whole retained delivery history. Every slot's
-- invocation belongs to an app of the slot's account. Limits are unchanged.
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
 FROM apps a
 JOIN invocations i ON i.app_id=a.id AND i.state IN ('pending','dispatching') AND i.id<>NEW.id
 JOIN event_delivery_slots s ON s.invocation_id=i.id AND s.account_id=a.account_id
 WHERE a.account_id=slot.account_id;
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
SET LOCAL lock_timeout = '5s';
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
