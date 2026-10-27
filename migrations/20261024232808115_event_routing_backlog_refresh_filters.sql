-- migration-compat: expand
-- filename: 20261024232808115_event_routing_backlog_refresh_filters.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-617 amendment 2026-10-25. The backlog projection refreshed on every
-- outbox and recipient change: eight refreshes per routed event (V12 trigger
-- profile). Settling a receipt that uses recipient claims refreshed every key,
-- though none reads the outbox's state, availability or lease once its
-- recipient row exists, and every recipient update refreshed its key whatever
-- column changed. The triggers now skip outbox moves no row reads, ignore
-- recipient columns the view does not read, and skip recipients deleted with
-- their receipt. They stay immediate; the view and the refresh statement are
-- unchanged.
CREATE OR REPLACE FUNCTION project_event_routing_backlog_root() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sub text; moved boolean;
BEGIN
    IF TG_OP='INSERT' OR (NEW.recipient_snapshot,NEW.recipient_claims,NEW.created_at,NEW.account_id)
        IS DISTINCT FROM (OLD.recipient_snapshot,OLD.recipient_claims,OLD.created_at,OLD.account_id) THEN
        PERFORM refresh_event_routing_backlog(NEW.id);
        RETURN NEW;
    END IF;
    moved := (NEW.state,NEW.available_at,NEW.lease_until) IS DISTINCT FROM (OLD.state,OLD.available_at,OLD.lease_until);
    IF moved AND NOT NEW.recipient_claims THEN
        PERFORM refresh_event_routing_backlog(NEW.id);
        RETURN NEW;
    END IF;
    -- With recipient claims a recipient that has its own row takes state,
    -- availability and lease from it, so an outbox move can change only
    -- captured recipients without a row yet; progress changes only its keys.
    FOR sub IN
        SELECT k FROM (
            SELECT jsonb_object_keys(NEW.recipient_progress) AS k
            UNION SELECT jsonb_object_keys(OLD.recipient_progress) AS k
        ) keys WHERE NEW.recipient_progress->k IS DISTINCT FROM OLD.recipient_progress->k
        UNION
        SELECT captured.recipient->>'id'
        FROM jsonb_array_elements(CASE WHEN moved THEN coalesce(NEW.recipient_snapshot,'[]'::jsonb) ELSE '[]'::jsonb END) captured(recipient)
        WHERE NOT EXISTS (SELECT 1 FROM event_fanout_recipients r
                          WHERE r.outbox_id=NEW.id AND r.subscription_id=captured.recipient->>'id')
        ORDER BY 1
    LOOP PERFORM refresh_event_routing_backlog(NEW.id,sub); END LOOP;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION project_event_routing_backlog_recipient() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        -- Deleting a receipt cascades its projection rows; only a recipient
        -- removed from a live receipt can change what remains.
        IF EXISTS (SELECT 1 FROM event_fanout_outbox WHERE id=OLD.outbox_id) THEN
            PERFORM refresh_event_routing_backlog(OLD.outbox_id,OLD.subscription_id);
        END IF;
        RETURN OLD;
    END IF;
    PERFORM refresh_event_routing_backlog(NEW.outbox_id,NEW.subscription_id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS event_routing_backlog_recipient ON event_fanout_recipients;
CREATE TRIGGER event_routing_backlog_recipient AFTER INSERT OR DELETE ON event_fanout_recipients
    FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_recipient();
-- The view reads these recipient columns; other updates (claim tokens,
-- generation counters, deadlines) cannot change the projection.
DROP TRIGGER IF EXISTS event_routing_backlog_recipient_update ON event_fanout_recipients;
CREATE TRIGGER event_routing_backlog_recipient_update
    AFTER UPDATE OF state, total_attempts, capacity_deferrals, available_at, lease_until, recipient, receipt_position, outbox_id, subscription_id
    ON event_fanout_recipients FOR EACH ROW
    WHEN ((OLD.state, OLD.total_attempts, OLD.capacity_deferrals, OLD.available_at, OLD.lease_until, OLD.recipient, OLD.receipt_position, OLD.outbox_id, OLD.subscription_id)
          IS DISTINCT FROM (NEW.state, NEW.total_attempts, NEW.capacity_deferrals, NEW.available_at, NEW.lease_until, NEW.recipient, NEW.receipt_position, NEW.outbox_id, NEW.subscription_id))
    EXECUTE FUNCTION project_event_routing_backlog_recipient();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS event_routing_backlog_recipient_update ON event_fanout_recipients;
DROP TRIGGER IF EXISTS event_routing_backlog_recipient ON event_fanout_recipients;
CREATE TRIGGER event_routing_backlog_recipient AFTER INSERT OR DELETE OR UPDATE ON event_fanout_recipients
    FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_recipient();

CREATE OR REPLACE FUNCTION project_event_routing_backlog_recipient() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        PERFORM refresh_event_routing_backlog(OLD.outbox_id,OLD.subscription_id);
        RETURN OLD;
    END IF;
    PERFORM refresh_event_routing_backlog(NEW.outbox_id,NEW.subscription_id);
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION project_event_routing_backlog_root() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sub text;
BEGIN
    IF TG_OP='INSERT' THEN
        PERFORM refresh_event_routing_backlog(NEW.id);
    ELSIF (NEW.recipient_snapshot,NEW.state,NEW.available_at,NEW.lease_until,NEW.recipient_claims,NEW.created_at,NEW.account_id)
       IS DISTINCT FROM (OLD.recipient_snapshot,OLD.state,OLD.available_at,OLD.lease_until,OLD.recipient_claims,OLD.created_at,OLD.account_id) THEN
        PERFORM refresh_event_routing_backlog(NEW.id);
    ELSE
        FOR sub IN SELECT k FROM (
            SELECT jsonb_object_keys(NEW.recipient_progress) AS k
            UNION SELECT jsonb_object_keys(OLD.recipient_progress) AS k
        ) keys WHERE NEW.recipient_progress->k IS DISTINCT FROM OLD.recipient_progress->k ORDER BY k
        LOOP PERFORM refresh_event_routing_backlog(NEW.id,sub); END LOOP;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
