-- migration-compat: expand
-- filename: 20261024232808115_event_routing_backlog_commit_refresh.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-617 amendment 2026-10-25. The backlog projection refreshed on every
-- outbox and recipient change: eight refreshes per routed event, 2.9 ms of
-- database time (V12 profile, docs/ops/evidence/20261025-event-routing-backlog-refresh).
-- Adoption and completion each change one key two or three times in a
-- transaction, and settling a receipt with recipient claims refreshed every
-- key though none reads the outbox's state, availability or lease once its
-- recipient row exists. The triggers now run at commit, refresh each key once
-- per firing, skip outbox moves no row reads, and ignore recipient columns the
-- view does not read. The view and the refresh statement are unchanged.

-- Refreshes a key at most once per trigger firing. Deferred triggers fire in
-- one batch at commit (or at SET CONSTRAINTS ... IMMEDIATE), after every change
-- they cover, so one refresh per key reflects all of them. A batch is the
-- statement that fires it; marks from an earlier batch never skip a later
-- one. Past 16 KiB of marks a large statement refreshes every key, as before.
CREATE OR REPLACE FUNCTION refresh_event_routing_backlog_once(p_outbox_id bigint,p_subscription_id text DEFAULT NULL)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    batch text := statement_timestamp()::text;
    done text := '';
    receipt text := chr(31) || p_outbox_id::text || chr(30);
    key text := receipt || coalesce(p_subscription_id,'');
BEGIN
    IF current_setting('faas.event_routing_backlog_batch',true) IS NOT DISTINCT FROM batch THEN
        done := coalesce(current_setting('faas.event_routing_backlog_refreshed',true),'');
    ELSE
        PERFORM set_config('faas.event_routing_backlog_batch',batch,true);
        PERFORM set_config('faas.event_routing_backlog_refreshed','',true);
    END IF;
    -- A receipt-wide refresh covers each of its keys.
    IF position(receipt || chr(31) IN done || chr(31)) > 0
       OR (p_subscription_id IS NOT NULL AND position(key || chr(31) IN done || chr(31)) > 0) THEN
        RETURN;
    END IF;
    PERFORM refresh_event_routing_backlog(p_outbox_id,p_subscription_id);
    IF length(done) < 16384 THEN
        PERFORM set_config('faas.event_routing_backlog_refreshed',done || key,true);
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION project_event_routing_backlog_root() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sub text; moved boolean;
BEGIN
    IF TG_OP='INSERT' OR (NEW.recipient_snapshot,NEW.recipient_claims,NEW.created_at,NEW.account_id)
        IS DISTINCT FROM (OLD.recipient_snapshot,OLD.recipient_claims,OLD.created_at,OLD.account_id) THEN
        PERFORM refresh_event_routing_backlog_once(NEW.id);
        RETURN NEW;
    END IF;
    moved := (NEW.state,NEW.available_at,NEW.lease_until) IS DISTINCT FROM (OLD.state,OLD.available_at,OLD.lease_until);
    IF moved AND NOT NEW.recipient_claims THEN
        PERFORM refresh_event_routing_backlog_once(NEW.id);
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
    LOOP PERFORM refresh_event_routing_backlog_once(NEW.id,sub); END LOOP;
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
            PERFORM refresh_event_routing_backlog_once(OLD.outbox_id,OLD.subscription_id);
        END IF;
        RETURN OLD;
    END IF;
    PERFORM refresh_event_routing_backlog_once(NEW.outbox_id,NEW.subscription_id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS event_routing_backlog_root ON event_fanout_outbox;
CREATE CONSTRAINT TRIGGER event_routing_backlog_root
    AFTER INSERT OR UPDATE OF recipient_snapshot, recipient_progress, state, available_at, lease_until, recipient_claims, created_at, account_id
    ON event_fanout_outbox DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_root();

DROP TRIGGER IF EXISTS event_routing_backlog_recipient ON event_fanout_recipients;
CREATE CONSTRAINT TRIGGER event_routing_backlog_recipient
    AFTER INSERT OR DELETE ON event_fanout_recipients DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_recipient();
-- The view reads these recipient columns; other updates (claim tokens,
-- generation counters, deadlines) cannot change the projection.
CREATE CONSTRAINT TRIGGER event_routing_backlog_recipient_update
    AFTER UPDATE OF state, total_attempts, capacity_deferrals, available_at, lease_until, recipient, receipt_position, outbox_id, subscription_id
    ON event_fanout_recipients DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
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
DROP TRIGGER IF EXISTS event_routing_backlog_root ON event_fanout_outbox;
CREATE TRIGGER event_routing_backlog_root
    AFTER INSERT OR UPDATE OF recipient_snapshot, recipient_progress, state, available_at, lease_until, recipient_claims, created_at, account_id
    ON event_fanout_outbox FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_root();

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

DROP FUNCTION IF EXISTS refresh_event_routing_backlog_once(bigint,text);
-- +goose StatementEnd
