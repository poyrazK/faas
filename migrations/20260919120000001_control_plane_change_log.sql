-- filename: 20260919120000001_control_plane_change_log.sql

-- Durable broadcast ledger for cache-backed control-plane mutations.
--
-- LISTEN/NOTIFY remains the low-latency path, but it is a lossy broadcast:
-- a gateway can miss the event while its connection is reconnecting. Unlike
-- notification_outbox (which is a single-consumer handoff), this ledger is
-- read independently by every gateway replica. The first slice records app
-- row mutations; future runtime-affecting resources can use the same table
-- without changing the consumer protocol.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS control_plane_change_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    resource_type text NOT NULL CHECK (resource_type <> ''),
    resource_id   uuid NOT NULL,
    app_id        uuid NOT NULL,
    operation     text NOT NULL CHECK (operation IN ('created', 'updated', 'deleted')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS control_plane_change_log_created_idx
    ON control_plane_change_log (created_at, id);

CREATE INDEX IF NOT EXISTS control_plane_change_log_app_idx
    ON control_plane_change_log (resource_type, app_id, id);

CREATE OR REPLACE FUNCTION apps_record_control_plane_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', NEW.id, NEW.id, 'created');
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', NEW.id, NEW.id, 'updated');
        RETURN NEW;
    ELSE
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', OLD.id, OLD.id, 'deleted');
        RETURN OLD;
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS apps_record_control_plane_change_trg ON apps;
CREATE TRIGGER apps_record_control_plane_change_trg
    AFTER INSERT OR UPDATE OR DELETE ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_record_control_plane_change();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_record_control_plane_change_trg ON apps;
DROP FUNCTION IF EXISTS apps_record_control_plane_change();
DROP TABLE IF EXISTS control_plane_change_log;
-- +goose StatementEnd
