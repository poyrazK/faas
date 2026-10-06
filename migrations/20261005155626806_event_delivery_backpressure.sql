-- filename: 20261005155626806_event_delivery_backpressure.sql
-- ADR-614: mutex and live-delivery identity are separate from retained receipts.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS event_delivery_capacity (
 account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 consumer_limit integer NOT NULL CHECK (consumer_limit > 0),
 app_limit integer NOT NULL CHECK (app_limit > 0),
 account_limit integer NOT NULL CHECK (account_limit > 0)
);
CREATE TABLE IF NOT EXISTS event_delivery_slots (
 invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES event_delivery_capacity(account_id) ON DELETE CASCADE,
 app_id uuid NOT NULL,
 subscription_id text NOT NULL
);
CREATE INDEX IF NOT EXISTS event_delivery_slots_consumer ON event_delivery_slots(account_id, app_id, subscription_id);
CREATE TABLE IF NOT EXISTS event_routing_fairness (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 subscription_id text NOT NULL,
 last_claimed_at timestamptz NOT NULL,
 PRIMARY KEY (account_id, subscription_id)
);
ALTER TABLE event_fanout_recipients ADD COLUMN IF NOT EXISTS capacity_deferrals integer NOT NULL DEFAULT 0 CHECK (capacity_deferrals >= 0);
ALTER TABLE event_fanout_recipients ADD COLUMN IF NOT EXISTS generation_capacity_deferrals integer NOT NULL DEFAULT 0 CHECK (generation_capacity_deferrals >= 0);

-- Replays inherit authority from the stored parent, never event headers.
-- Only entry into live work takes the mutex. Exits cannot invert admission's
-- mutex -> pending invocation lock order, and an uncommitted exit still counts.
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
DROP TRIGGER IF EXISTS event_delivery_replay_capacity ON invocations;
CREATE TRIGGER event_delivery_replay_capacity AFTER INSERT OR UPDATE OF state ON invocations
 FOR EACH ROW EXECUTE FUNCTION guard_event_delivery_replay();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER event_delivery_replay_capacity ON invocations;
DROP FUNCTION guard_event_delivery_replay();
ALTER TABLE event_fanout_recipients DROP COLUMN generation_capacity_deferrals;
ALTER TABLE event_fanout_recipients DROP COLUMN capacity_deferrals;
DROP TABLE event_routing_fairness;
DROP TABLE event_delivery_slots;
DROP TABLE event_delivery_capacity;
-- +goose StatementEnd
