-- +goose Up
-- +goose StatementBegin
-- ADR-161: heartbeat-only and no-op updates do not change routing. Emitting
-- these notifications closes cached gRPC connections, aborting active wakes.
-- Compare the complete row except liveness so future configuration columns
-- remain invalidating by default. Lifecycle and key rotation are not exempt.
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_node_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb;
BEGIN
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
