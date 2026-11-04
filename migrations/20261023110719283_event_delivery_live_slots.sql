-- migration-compat: expand
-- filename: 20261023110719283_event_delivery_live_slots.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-614 delivery limits count an account's live deliveries on every event
-- admission and replay. A slot lives as long as its invocation is retained,
-- so replays can inherit it, and the count read every slot of the account
-- before filtering on the invocation's state. With a backlog in other
-- accounts the planner scans the whole slot table (117-146 ms per admission
-- at 500k slots; docs/ops/evidence/20261022-v12-provisional). A slot now
-- says whether its delivery is live; the count reads the account's live
-- slots through a partial index (20261023110719517) and still checks each
-- invocation's state, so a slot marked live too long only costs a lookup.
ALTER TABLE event_delivery_slots ADD COLUMN IF NOT EXISTS live boolean NOT NULL DEFAULT true;

-- An account with a long history has few live slots. Column statistics alone
-- multiply the account's share of slots by the live share, overestimate its
-- live slots, and plan a scan of every live delivery on the platform (8-10 ms
-- with 10k-50k live elsewhere); joint statistics plan the live index (0.3 ms).
CREATE STATISTICS IF NOT EXISTS event_delivery_slots_account_live (mcv, dependencies)
    ON account_id, live FROM event_delivery_slots;

CREATE OR REPLACE FUNCTION stamp_event_delivery_slot_live() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.live := EXISTS (SELECT 1 FROM invocations i
        WHERE i.id = NEW.invocation_id AND i.state IN ('pending', 'dispatching'));
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION sync_event_delivery_slot_live() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE event_delivery_slots SET live = NEW.state IN ('pending', 'dispatching')
    WHERE invocation_id = NEW.id AND live IS DISTINCT FROM (NEW.state IN ('pending', 'dispatching'));
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS event_delivery_slot_live ON event_delivery_slots;
CREATE TRIGGER event_delivery_slot_live BEFORE INSERT ON event_delivery_slots
    FOR EACH ROW EXECUTE FUNCTION stamp_event_delivery_slot_live();

-- Fires only when a delivery becomes live or stops being live.
DROP TRIGGER IF EXISTS event_delivery_slot_live_sync ON invocations;
CREATE TRIGGER event_delivery_slot_live_sync AFTER UPDATE OF state ON invocations
    FOR EACH ROW WHEN ((OLD.state IN ('pending', 'dispatching')) IS DISTINCT FROM (NEW.state IN ('pending', 'dispatching')))
    EXECUTE FUNCTION sync_event_delivery_slot_live();

CREATE OR REPLACE FUNCTION guard_event_delivery_replay() RETURNS trigger
LANGUAGE plpgsql AS $$
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
 WHERE s.account_id=slot.account_id AND s.live AND i.state IN ('pending','dispatching') AND i.id<>NEW.id;
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
CREATE OR REPLACE FUNCTION guard_event_delivery_replay() RETURNS trigger
LANGUAGE plpgsql AS $$
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
DROP TRIGGER IF EXISTS event_delivery_slot_live_sync ON invocations;
DROP TRIGGER IF EXISTS event_delivery_slot_live ON event_delivery_slots;
DROP FUNCTION IF EXISTS sync_event_delivery_slot_live();
DROP FUNCTION IF EXISTS stamp_event_delivery_slot_live();
DROP STATISTICS IF EXISTS event_delivery_slots_account_live;
ALTER TABLE event_delivery_slots DROP COLUMN IF EXISTS live;
-- +goose StatementEnd
