-- filename: 20260923190100001_instance_readiness_notify.sql
-- +goose Up
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS events_instance_readiness_latest_idx
    ON events ((data->>'instance_id'), at DESC, id DESC)
    WHERE kind = 'wake.sidecar_health'
      AND data->>'status' IN ('ready', 'unready');

CREATE OR REPLACE FUNCTION instance_readiness_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.kind = 'wake.sidecar_health'
       AND NEW.data->>'status' IN ('ready', 'unready')
       AND coalesce(NEW.data->>'app_id', '') <> ''
       AND coalesce(NEW.data->>'instance_id', '') <> '' THEN
        PERFORM pg_notify('instance_readiness_changed', json_build_object(
            'app_id', NEW.data->>'app_id',
            'instance_id', NEW.data->>'instance_id',
            'status', NEW.data->>'status',
            'at', NEW.at,
            'event_id', NEW.id
        )::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER instance_readiness_notify_trg
AFTER INSERT ON events
FOR EACH ROW EXECUTE FUNCTION instance_readiness_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS instance_readiness_notify_trg ON events;
DROP FUNCTION IF EXISTS instance_readiness_notify();
DROP INDEX IF EXISTS public.events_instance_readiness_latest_idx;
-- +goose StatementEnd
