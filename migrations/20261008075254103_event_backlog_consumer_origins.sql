-- filename: 20261008075254103_event_backlog_consumer_origins.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_routing_backlog
    ADD COLUMN consumer_kind text NOT NULL DEFAULT 'application'
        CHECK (consumer_kind IN ('application','workflow')),
    ADD COLUMN origin text NOT NULL DEFAULT 'acceptance'
        CHECK (origin IN ('acceptance','backfill')),
    ADD COLUMN workflow_name text NOT NULL DEFAULT '';

CREATE INDEX event_routing_backlog_kind_origin_age
    ON event_routing_backlog(account_id,consumer_kind,origin,accepted_at,outbox_id,subscription_id);

CREATE OR REPLACE VIEW event_routing_backlog_source AS
SELECT o.id AS outbox_id, s.recipient->>'id' AS subscription_id, o.account_id,
       (s.recipient->>'app_id')::uuid AS app_id, o.created_at AS accepted_at,
       CASE WHEN s.origin='backfill' OR o.recipient_claims THEN 'recipient' ELSE 'event' END::text AS routing_mode,
       effective.routing_state,
       CASE WHEN effective.routing_state='pending' THEN coalesce(p.progress->>'capacity_scope','') ELSE '' END::text AS capacity_scope,
       CASE WHEN s.origin='backfill' OR o.recipient_claims THEN coalesce(r.total_attempts,(p.progress->>'attempts')::integer,0)
            ELSE coalesce((p.progress->>'attempts')::integer,0) END::integer AS attempts,
       greatest(coalesce((p.progress->>'capacity_deferrals')::integer,0),
                CASE WHEN s.origin='backfill' OR o.recipient_claims THEN coalesce(r.capacity_deferrals,0) ELSE 0 END)::integer AS capacity_deferrals,
       CASE WHEN effective.routing_state='pending' THEN
            CASE WHEN s.origin='backfill' OR o.recipient_claims THEN coalesce(r.available_at,(p.progress->>'next_attempt_at')::timestamptz,o.available_at)
                 ELSE coalesce((p.progress->>'next_attempt_at')::timestamptz,CASE WHEN o.state='pending' THEN o.available_at END) END
       END::timestamptz AS next_attempt_at,
       CASE WHEN s.origin='backfill' OR o.recipient_claims THEN r.lease_until ELSE o.lease_until END::timestamptz AS lease_until,
       s.origin::text AS origin,
       CASE WHEN s.recipient->'workflow' IS NOT NULL AND s.recipient->'workflow'<>'null'::jsonb THEN 'workflow' ELSE 'application' END::text AS consumer_kind,
       coalesce(s.recipient->'workflow'->>'name','')::text AS workflow_name
FROM event_fanout_outbox o
CROSS JOIN LATERAL (
    SELECT captured.recipient, 'acceptance'::text AS origin
    FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) captured(recipient)
    UNION ALL
    SELECT added.recipient, 'backfill'::text
    FROM event_fanout_recipients added
    WHERE added.outbox_id=o.id AND added.receipt_position IS NOT NULL
) s
CROSS JOIN LATERAL (SELECT coalesce(o.recipient_progress->(s.recipient->>'id'),'{}'::jsonb) AS progress) p
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
CROSS JOIN LATERAL (
    SELECT CASE WHEN s.origin='backfill' OR o.recipient_claims THEN coalesce(r.state,p.progress->>'state','pending')
                ELSE coalesce(p.progress->>'state','pending') END::text AS routing_state
) effective
WHERE nullif(s.recipient->>'app_id','') IS NOT NULL
  AND effective.routing_state IN ('pending','processing');

CREATE OR REPLACE FUNCTION refresh_event_routing_backlog(p_outbox_id bigint,p_subscription_id text DEFAULT NULL)
RETURNS void LANGUAGE sql AS $$
WITH candidates AS MATERIALIZED (
    SELECT * FROM event_routing_backlog_source
    WHERE outbox_id=p_outbox_id AND (p_subscription_id IS NULL OR subscription_id=p_subscription_id)
), inserted AS (
    INSERT INTO event_routing_backlog
        (outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
         attempts,capacity_deferrals,next_attempt_at,lease_until,consumer_kind,origin,workflow_name)
    SELECT outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
           attempts,capacity_deferrals,next_attempt_at,lease_until,consumer_kind,origin,workflow_name
    FROM candidates ORDER BY subscription_id
    ON CONFLICT (outbox_id,subscription_id) DO UPDATE SET
        account_id=excluded.account_id,app_id=excluded.app_id,accepted_at=excluded.accepted_at,
        routing_mode=excluded.routing_mode,routing_state=excluded.routing_state,capacity_scope=excluded.capacity_scope,
        attempts=excluded.attempts,capacity_deferrals=excluded.capacity_deferrals,
        next_attempt_at=excluded.next_attempt_at,lease_until=excluded.lease_until,
        consumer_kind=excluded.consumer_kind,origin=excluded.origin,workflow_name=excluded.workflow_name
    WHERE (event_routing_backlog.account_id,event_routing_backlog.app_id,event_routing_backlog.accepted_at,
           event_routing_backlog.routing_mode,event_routing_backlog.routing_state,event_routing_backlog.capacity_scope,
           event_routing_backlog.attempts,event_routing_backlog.capacity_deferrals,event_routing_backlog.next_attempt_at,
           event_routing_backlog.lease_until,event_routing_backlog.consumer_kind,event_routing_backlog.origin,
           event_routing_backlog.workflow_name)
       IS DISTINCT FROM (excluded.account_id,excluded.app_id,excluded.accepted_at,excluded.routing_mode,excluded.routing_state,
                         excluded.capacity_scope,excluded.attempts,excluded.capacity_deferrals,excluded.next_attempt_at,
                         excluded.lease_until,excluded.consumer_kind,excluded.origin,excluded.workflow_name)
)
DELETE FROM event_routing_backlog b WHERE b.outbox_id=p_outbox_id
    AND (p_subscription_id IS NULL OR b.subscription_id=p_subscription_id)
    AND NOT EXISTS (SELECT 1 FROM candidates c WHERE c.subscription_id=b.subscription_id);
$$;

-- Rebuild the metadata projection once so existing captured workflows and
-- retained backfill recipients become visible immediately after migration.
INSERT INTO event_routing_backlog
    (outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
     attempts,capacity_deferrals,next_attempt_at,lease_until,consumer_kind,origin,workflow_name)
SELECT outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
       attempts,capacity_deferrals,next_attempt_at,lease_until,consumer_kind,origin,workflow_name
FROM event_routing_backlog_source
ON CONFLICT (outbox_id,subscription_id) DO UPDATE SET
    routing_mode=excluded.routing_mode,routing_state=excluded.routing_state,capacity_scope=excluded.capacity_scope,
    attempts=excluded.attempts,capacity_deferrals=excluded.capacity_deferrals,
    next_attempt_at=excluded.next_attempt_at,lease_until=excluded.lease_until,
    consumer_kind=excluded.consumer_kind,origin=excluded.origin,workflow_name=excluded.workflow_name;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM event_routing_backlog WHERE consumer_kind='workflow' OR origin='backfill') THEN
        RAISE EXCEPTION 'workflow and backfill backlog rows must settle before rollback';
    END IF;
END $$;
DROP INDEX event_routing_backlog_kind_origin_age;

-- Keep the prior application-only projection on rollback.
DROP TRIGGER IF EXISTS event_routing_backlog_recipient ON event_fanout_recipients;
DROP TRIGGER IF EXISTS event_routing_backlog_root ON event_fanout_outbox;
DROP FUNCTION IF EXISTS project_event_routing_backlog_recipient();
DROP FUNCTION IF EXISTS project_event_routing_backlog_root();
DROP FUNCTION IF EXISTS refresh_event_routing_backlog(bigint,text);
DROP VIEW event_routing_backlog_source;
ALTER TABLE event_routing_backlog DROP COLUMN workflow_name,DROP COLUMN origin,DROP COLUMN consumer_kind;
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
       CASE WHEN o.recipient_claims THEN r.lease_until ELSE o.lease_until END::timestamptz AS lease_until,
       'acceptance'::text AS origin, 'application'::text AS consumer_kind, ''::text AS workflow_name
FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(o.recipient_snapshot) s(recipient)
CROSS JOIN LATERAL (SELECT coalesce(o.recipient_progress->(s.recipient->>'id'),'{}'::jsonb) AS progress) p
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
CROSS JOIN LATERAL (SELECT CASE WHEN o.recipient_claims THEN coalesce(r.state,p.progress->>'state','pending')
                               ELSE coalesce(p.progress->>'state','pending') END::text AS routing_state) effective
WHERE o.state IN ('pending','processing') AND nullif(s.recipient->>'app_id','') IS NOT NULL
    AND (s.recipient->'workflow' IS NULL OR s.recipient->'workflow'='null'::jsonb);

CREATE OR REPLACE FUNCTION refresh_event_routing_backlog(p_outbox_id bigint,p_subscription_id text DEFAULT NULL)
RETURNS void LANGUAGE sql AS $$
WITH candidates AS MATERIALIZED (
    SELECT * FROM event_routing_backlog_source
    WHERE outbox_id=p_outbox_id AND (p_subscription_id IS NULL OR subscription_id=p_subscription_id)
      AND routing_state IN ('pending','processing')
), inserted AS (
    INSERT INTO event_routing_backlog
        (outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
         attempts,capacity_deferrals,next_attempt_at,lease_until)
    SELECT outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
           attempts,capacity_deferrals,next_attempt_at,lease_until
    FROM candidates ORDER BY subscription_id
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

CREATE OR REPLACE FUNCTION project_event_routing_backlog_root() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION project_event_routing_backlog_recipient() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementEnd
