-- migration-compat: expand
-- filename: 20261021223015842_event_order_lane_column.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-795 ordering lanes are (app, work policy, key). A recipient row now
-- carries its lane id, so the ordering check can find earlier deliveries in
-- a lane through an index (20261021223015867, 20261021223015891). The
-- database sets it, so rows written by any scheduler version carry it; a
-- row without one gets it on its next write.
ALTER TABLE event_fanout_recipients ADD COLUMN IF NOT EXISTS order_lane bigint;

CREATE OR REPLACE FUNCTION set_event_recipient_order_lane() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.order_lane := event_order_lane_lock_id(NEW.recipient->>'app_id', NEW.recipient->'work'->>'policy_name',
        (SELECT o.payload FROM event_fanout_outbox o WHERE o.id = NEW.outbox_id), NEW.recipient->'work'->>'key_selector');
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS event_fanout_recipients_order_lane ON event_fanout_recipients;
CREATE TRIGGER event_fanout_recipients_order_lane BEFORE INSERT OR UPDATE ON event_fanout_recipients
    FOR EACH ROW WHEN (NEW.order_lane IS NULL AND coalesce(NEW.recipient->'work'->>'policy_name', '') <> '')
    EXECUTE FUNCTION set_event_recipient_order_lane();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS event_fanout_recipients_order_lane ON event_fanout_recipients;
DROP FUNCTION IF EXISTS set_event_recipient_order_lane();
ALTER TABLE event_fanout_recipients DROP COLUMN IF EXISTS order_lane;
-- +goose StatementEnd
