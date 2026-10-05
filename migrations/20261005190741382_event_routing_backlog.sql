-- filename: 20261005190741382_event_routing_backlog.sql

-- +goose Up
-- +goose StatementBegin
-- A metadata-only read model; routing checkpoints remain authoritative.
CREATE TABLE event_routing_backlog (
    outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE,
    subscription_id text NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    accepted_at timestamptz NOT NULL,
    routing_mode text NOT NULL CHECK (routing_mode IN ('event','recipient')),
    routing_state text NOT NULL CHECK (routing_state IN ('pending','processing')),
    capacity_scope text NOT NULL CHECK (capacity_scope IN ('','consumer','app','account')),
    attempts integer NOT NULL CHECK (attempts >= 0),
    capacity_deferrals integer NOT NULL CHECK (capacity_deferrals >= 0),
    next_attempt_at timestamptz,
    lease_until timestamptz,
    PRIMARY KEY (outbox_id,subscription_id)
);
CREATE INDEX event_routing_backlog_account_age ON event_routing_backlog(account_id,accepted_at,outbox_id,subscription_id);
CREATE INDEX event_routing_backlog_consumer_age ON event_routing_backlog(account_id,app_id,subscription_id,accepted_at,outbox_id);
CREATE INDEX event_outbox_unattributed_age ON event_fanout_outbox(account_id,created_at,id)
    WHERE recipient_snapshot IS NULL AND state IN ('pending','processing');

CREATE VIEW event_routing_backlog_source AS
SELECT o.id AS outbox_id, s.recipient->>'id' AS subscription_id, o.account_id,
       (s.recipient->>'app_id')::uuid AS app_id, o.created_at AS accepted_at,
       CASE WHEN o.recipient_claims THEN 'recipient' ELSE 'event' END::text AS routing_mode,
       effective.routing_state,
       CASE WHEN effective.routing_state='pending' THEN coalesce(p.progress->>'capacity_scope','') ELSE '' END::text AS capacity_scope,
       CASE WHEN o.recipient_claims THEN coalesce(r.total_attempts,(p.progress->>'attempts')::integer,0)
            ELSE coalesce((p.progress->>'attempts')::integer,0) END::integer AS attempts,
       greatest(coalesce((p.progress->>'capacity_deferrals')::integer,0),
                CASE WHEN o.recipient_claims THEN coalesce(r.capacity_deferrals,0) ELSE 0 END)::integer AS capacity_deferrals,
       CASE WHEN effective.routing_state='pending' THEN
            CASE WHEN o.recipient_claims THEN coalesce(r.available_at,(p.progress->>'next_attempt_at')::timestamptz,o.available_at)
                 ELSE coalesce((p.progress->>'next_attempt_at')::timestamptz,CASE WHEN o.state='pending' THEN o.available_at END) END
       END::timestamptz AS next_attempt_at,
       CASE WHEN o.recipient_claims THEN r.lease_until ELSE o.lease_until END::timestamptz AS lease_until
FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(o.recipient_snapshot) s(recipient)
CROSS JOIN LATERAL (SELECT coalesce(o.recipient_progress->(s.recipient->>'id'),'{}'::jsonb) AS progress) p
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
CROSS JOIN LATERAL (SELECT CASE WHEN o.recipient_claims THEN coalesce(r.state,p.progress->>'state','pending')
                               ELSE coalesce(p.progress->>'state','pending') END::text AS routing_state) effective
WHERE o.state IN ('pending','processing') AND nullif(s.recipient->>'app_id','') IS NOT NULL
    AND (s.recipient->'workflow' IS NULL OR s.recipient->'workflow'='null'::jsonb);

CREATE FUNCTION refresh_event_routing_backlog(p_outbox_id bigint,p_subscription_id text DEFAULT NULL)
RETURNS void LANGUAGE sql AS $$
WITH candidates AS MATERIALIZED (
    SELECT * FROM event_routing_backlog_source
    WHERE outbox_id=p_outbox_id AND (p_subscription_id IS NULL OR subscription_id=p_subscription_id)
      AND routing_state IN ('pending','processing')
), inserted AS (
    INSERT INTO event_routing_backlog SELECT * FROM candidates ORDER BY subscription_id
    ON CONFLICT (outbox_id,subscription_id) DO UPDATE SET
        account_id=excluded.account_id,app_id=excluded.app_id,accepted_at=excluded.accepted_at,
        routing_mode=excluded.routing_mode,routing_state=excluded.routing_state,capacity_scope=excluded.capacity_scope,
        attempts=excluded.attempts,capacity_deferrals=excluded.capacity_deferrals,
        next_attempt_at=excluded.next_attempt_at,lease_until=excluded.lease_until
    WHERE (event_routing_backlog.account_id,event_routing_backlog.app_id,event_routing_backlog.accepted_at,
           event_routing_backlog.routing_mode,event_routing_backlog.routing_state,event_routing_backlog.capacity_scope,
           event_routing_backlog.attempts,event_routing_backlog.capacity_deferrals,event_routing_backlog.next_attempt_at,event_routing_backlog.lease_until)
       IS DISTINCT FROM (excluded.account_id,excluded.app_id,excluded.accepted_at,excluded.routing_mode,excluded.routing_state,
                         excluded.capacity_scope,excluded.attempts,excluded.capacity_deferrals,excluded.next_attempt_at,excluded.lease_until)
)
DELETE FROM event_routing_backlog b WHERE b.outbox_id=p_outbox_id
    AND (p_subscription_id IS NULL OR b.subscription_id=p_subscription_id)
    AND NOT EXISTS (SELECT 1 FROM candidates c WHERE c.subscription_id=b.subscription_id);
$$;

CREATE FUNCTION project_event_routing_backlog_root() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER event_routing_backlog_root AFTER INSERT OR UPDATE OF recipient_snapshot,recipient_progress,state,available_at,lease_until,recipient_claims,created_at,account_id
    ON event_fanout_outbox FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_root();

CREATE FUNCTION project_event_routing_backlog_recipient() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        PERFORM refresh_event_routing_backlog(OLD.outbox_id,OLD.subscription_id);
        RETURN OLD;
    END IF;
    PERFORM refresh_event_routing_backlog(NEW.outbox_id,NEW.subscription_id);
    RETURN NEW;
END;
$$;
CREATE TRIGGER event_routing_backlog_recipient AFTER INSERT OR UPDATE OR DELETE
    ON event_fanout_recipients FOR EACH ROW EXECUTE FUNCTION project_event_routing_backlog_recipient();

INSERT INTO event_routing_backlog SELECT * FROM event_routing_backlog_source WHERE routing_state IN ('pending','processing');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER event_routing_backlog_recipient ON event_fanout_recipients;
DROP TRIGGER event_routing_backlog_root ON event_fanout_outbox;
DROP FUNCTION project_event_routing_backlog_recipient();
DROP FUNCTION project_event_routing_backlog_root();
DROP FUNCTION refresh_event_routing_backlog(bigint,text);
DROP VIEW event_routing_backlog_source;
DROP TABLE event_routing_backlog;
DROP INDEX event_outbox_unattributed_age;
-- +goose StatementEnd
