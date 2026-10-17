-- filename: 20261002173300001_readiness_target_identity.sql
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION instance_readiness_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.kind IN ('wake.sidecar_health', 'wake.app_readiness')
       AND NEW.data->>'status' IN ('ready', 'unready')
       AND coalesce(NEW.data->>'app_id', '') <> ''
       AND coalesce(NEW.data->>'instance_id', '') <> '' THEN
        PERFORM pg_notify('instance_readiness_changed', json_build_object(
            'app_id', NEW.data->>'app_id',
            'instance_id', NEW.data->>'instance_id',
            'wake_id', coalesce(NEW.data->>'wake_id', ''),
            'node_id', coalesce(NEW.data->>'node_id', ''),
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
