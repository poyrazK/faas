-- filename: 20261128073114100_apps_route_of_app.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261121170000 amendment 6: a route split's app serves only through
-- its source app's route rule. route_of_app_id names that source; the
-- gateway refuses a request that reaches the app any other way (its
-- platform host, a deployment URL, an alias, a tenant surface or a custom
-- domain). NULL for every other app.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS route_of_app_id uuid;

-- Gateways cache apps; tell them when an app becomes (or stops being) a
-- route app, on the app_changed payload contract.
CREATE OR REPLACE FUNCTION apps_route_of_app_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.route_of_app_id IS DISTINCT FROM OLD.route_of_app_id THEN
        PERFORM pg_notify(
            'app_changed',
            json_build_object('kind', 'updated', 'app_id', NEW.id::text)::text
        );
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE TRIGGER apps_route_of_app_notify_trg AFTER UPDATE OF route_of_app_id ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_route_of_app_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_route_of_app_notify_trg ON apps;
DROP FUNCTION IF EXISTS apps_route_of_app_notify();
ALTER TABLE apps DROP COLUMN IF EXISTS route_of_app_id;
-- +goose StatementEnd
