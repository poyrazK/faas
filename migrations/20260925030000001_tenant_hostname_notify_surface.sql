-- +goose Up
-- +goose StatementBegin
-- Hostname mutations must wake the parent surface's cert worker. The original
-- shared trigger emitted NEW.id for hostname INSERT/UPDATE and attempted
-- OLD.surface_id for surface DELETE, so both payloads could be wrong.
CREATE OR REPLACE FUNCTION notify_tenant_surface_changed() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    surface_uuid uuid;
BEGIN
    IF TG_TABLE_NAME = 'tenant_hostnames' THEN
        IF TG_OP = 'DELETE' THEN
            surface_uuid := OLD.surface_id;
        ELSE
            surface_uuid := NEW.surface_id;
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        surface_uuid := OLD.id;
    ELSE
        surface_uuid := NEW.id;
    END IF;
    PERFORM pg_notify('tenant_surface_changed', surface_uuid::text);
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_tenant_surface_changed() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('tenant_surface_changed', OLD.surface_id::text);
        RETURN OLD;
    ELSIF TG_OP = 'UPDATE' THEN
        PERFORM pg_notify('tenant_surface_changed', NEW.id::text);
        RETURN NEW;
    ELSE
        PERFORM pg_notify('tenant_surface_changed', NEW.id::text);
        RETURN NEW;
    END IF;
END
$$;
-- +goose StatementEnd
