-- filename: 20260925060000001_app_readiness_event.sql
-- +goose Up
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS events_app_readiness_latest_idx
    ON events ((data->>'instance_id'), at DESC, id DESC)
    WHERE kind = 'wake.app_readiness'
      AND data->>'status' IN ('ready', 'unready');

CREATE OR REPLACE FUNCTION instance_readiness_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.kind IN ('wake.sidecar_health', 'wake.app_readiness')
       AND NEW.data->>'status' IN ('ready', 'unready')
       AND coalesce(NEW.data->>'app_id', '') <> ''
       AND coalesce(NEW.data->>'instance_id', '') <> '' THEN
        PERFORM pg_notify('instance_readiness_changed', json_build_object(
            'app_id', NEW.data->>'app_id',
            'instance_id', NEW.data->>'instance_id',
            'source', CASE
                WHEN NEW.kind = 'wake.app_readiness' THEN 'primary_app'
                ELSE 'sidecar:' || coalesce(NEW.data->>'sidecar_name', '')
            END,
            'status', NEW.data->>'status',
            'at', NEW.at,
            'event_id', NEW.id
        )::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.events_app_readiness_latest_idx;

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
-- +goose StatementEnd
