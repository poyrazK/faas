-- filename: 20261102082209460_snapshot_fanout_notify.sql
-- migration-compat: expand

-- +goose Up
-- vmmd's snapshot replica worker asked Postgres every 100 ms whether a new
-- snapshot had fanned out to its node: 20 reads a second on prod's two nodes
-- (2026-11-02), nearly all finding nothing, and ten more per added node. A
-- new fan-out event now also sends snapshot_fanout, so the worker can sleep
-- until one arrives and keep a slow tick for retries and revalidation. The
-- payload is empty: the worker rereads its cursor. An older vmmd does not
-- listen and keeps its 100 ms tick.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION snapshot_fanout_event_on_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.storage_key <> '' AND NOT NEW.stale THEN
        INSERT INTO snapshot_fanout_events (snapshot_id)
        VALUES (NEW.id)
        ON CONFLICT (snapshot_id) DO NOTHING;
        IF FOUND THEN
            PERFORM pg_notify('snapshot_fanout', '');
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION snapshot_fanout_event_on_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.storage_key <> '' AND NOT NEW.stale THEN
        INSERT INTO snapshot_fanout_events (snapshot_id)
        VALUES (NEW.id)
        ON CONFLICT (snapshot_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
