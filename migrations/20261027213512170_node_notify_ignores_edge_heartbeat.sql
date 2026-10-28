-- filename: 20261027213512170_node_notify_ignores_edge_heartbeat.sql
-- ADR-161 extended: edge_listener_heartbeat_at (ADR-091 amendment
-- 2026-10-27, #5648) is liveness, like last_heartbeat_at. Each
-- gatewayd-internal stamps it every 10 s; before this change every stamp
-- fired compute_node_changed, and every subscriber closed its cached gRPC
-- client for that node, aborting in-flight wakes (production rc.299,
-- 2026-10-27: ~540 client evictions an hour per gateway, wakes failing
-- "context canceled" and surfacing as 503 capacity_unavailable).
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_node_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF (to_jsonb(NEW) - 'last_heartbeat_at' - 'edge_listener_heartbeat_at') IS NOT DISTINCT FROM
           (to_jsonb(OLD) - 'last_heartbeat_at' - 'edge_listener_heartbeat_at') THEN
            RETURN NEW;
        END IF;
    END IF;
    payload := jsonb_build_object(
        'node_id', NEW.id::text,
        'active', (NEW.lifecycle IN ('active', 'recovering')),
        'lifecycle', NEW.lifecycle::text
    );
    PERFORM pg_notify('compute_node_changed', payload::text);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_node_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF (to_jsonb(NEW) - 'last_heartbeat_at') IS NOT DISTINCT FROM
           (to_jsonb(OLD) - 'last_heartbeat_at') THEN
            RETURN NEW;
        END IF;
    END IF;
    payload := jsonb_build_object(
        'node_id', NEW.id::text,
        'active', (NEW.lifecycle IN ('active', 'recovering')),
        'lifecycle', NEW.lifecycle::text
    );
    PERFORM pg_notify('compute_node_changed', payload::text);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
