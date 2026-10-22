-- migration-compat: expand
-- filename: 20261021190512417_event_routing_backlog_refresh_plan_cache.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- The backlog projection triggers call this function on every outbox and
-- recipient change, about seven times per routed event. PostgreSQL 16 parses,
-- rewrites and plans a SQL-language function body on every call; for this
-- statement over event_routing_backlog_source that cost ~1.5 ms against
-- 0.2 ms of execution (V12 profile,
-- docs/ops/evidence/20261021-event-fanout-batch-claims). PL/pgSQL caches the
-- plan per session. The statement is unchanged.
CREATE OR REPLACE FUNCTION refresh_event_routing_backlog(p_outbox_id bigint,p_subscription_id text DEFAULT NULL)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
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
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
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
-- +goose StatementEnd
