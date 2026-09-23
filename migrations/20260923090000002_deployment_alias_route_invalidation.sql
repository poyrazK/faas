-- filename: 20260923090000002_deployment_alias_route_invalidation.sql
-- +goose Up
-- +goose StatementBegin

-- Alias mutations change the deployment pinned to a public hostname. Publish
-- the app-wide route invalidation in the same transaction as the mapping so
-- every gateway drops its cached host target only after the write commits.
CREATE FUNCTION notify_deployment_alias_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('app_changed', OLD.app_id::text);
    ELSE
        IF TG_OP = 'UPDATE' AND OLD.app_id IS DISTINCT FROM NEW.app_id THEN
            PERFORM pg_notify('app_changed', OLD.app_id::text);
        END IF;
        PERFORM pg_notify('app_changed', NEW.app_id::text);
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER deployment_aliases_app_changed
AFTER INSERT OR UPDATE OR DELETE ON deployment_aliases
FOR EACH ROW EXECUTE FUNCTION notify_deployment_alias_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployment_aliases_app_changed ON deployment_aliases;
DROP FUNCTION IF EXISTS notify_deployment_alias_changed();
-- +goose StatementEnd
