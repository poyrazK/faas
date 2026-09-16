-- +goose Up
-- +goose StatementBegin

-- app_changed has one canonical JSON contract. Consumers retain support for
-- the legacy bare UUID during rolling upgrades, but every database producer
-- emits kind + app_id after this migration lands.
CREATE OR REPLACE FUNCTION apps_maintenance_mode_notify() RETURNS trigger AS $$
BEGIN
    IF (TG_OP = 'UPDATE' AND NEW.maintenance_mode IS DISTINCT FROM OLD.maintenance_mode) THEN
        PERFORM pg_notify(
            'app_changed',
            json_build_object('kind', 'updated', 'app_id', NEW.id::text)::text
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION apps_declared_routes_policy_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.only_declared_routes IS DISTINCT FROM OLD.only_declared_routes
       OR NEW.declared_routes IS DISTINCT FROM OLD.declared_routes THEN
        PERFORM pg_notify(
            'app_changed',
            json_build_object('kind', 'updated', 'app_id', NEW.id::text)::text
        );
    END IF;
    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION apps_maintenance_mode_notify() RETURNS trigger AS $$
BEGIN
    IF (TG_OP = 'UPDATE' AND NEW.maintenance_mode IS DISTINCT FROM OLD.maintenance_mode) THEN
        PERFORM pg_notify('app_changed', NEW.id::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION apps_declared_routes_policy_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.only_declared_routes IS DISTINCT FROM OLD.only_declared_routes
       OR NEW.declared_routes IS DISTINCT FROM OLD.declared_routes THEN
        PERFORM pg_notify('app_changed', NEW.id::text);
    END IF;
    RETURN NEW;
END;
$$;

-- +goose StatementEnd
