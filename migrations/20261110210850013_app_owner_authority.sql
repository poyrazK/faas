-- filename: 20261110210850013_app_owner_authority.sql
-- migration-compat: expand
-- ADR-20261110210000 §4: an app's owner node is the control plane's decision.
-- Every compute daemon logs in as a faas_runtime member with row DML on
-- apps, so a rooted node could stamp itself the owner of any app. This
-- trigger admits an owner change (apps.node_id set or moved to a node) only
-- from a login that is a member of faas_owner_authority, the control plane's
-- schedd (deploy/ansible/roles/postgres/files/runtime-roles.sql).
--
-- app_owner_authority.mode:
--   any      no check (default; today's behaviour)
--   observe  every owner change is recorded in app_owner_writes, with whether
--            its login was authorized; nothing is refused
--   enforce  an unauthorized owner change fails with SQLSTATE 42501 and a
--            LOG line "faas_app_owner_refused" (the refusal rolls the audit
--            row back with the statement)
-- Clearing an owner (node_id NULL) is always admitted: it hands the app back
-- to the control plane's claim.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_owner_authority (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    mode text NOT NULL DEFAULT 'any' CHECK (mode IN ('any', 'observe', 'enforce')),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);
INSERT INTO app_owner_authority (singleton) VALUES (true) ON CONFLICT DO NOTHING;

-- No foreign key: the record outlives the app.
CREATE TABLE IF NOT EXISTS app_owner_writes (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    app_id uuid NOT NULL,
    from_node_id uuid,
    to_node_id uuid NOT NULL,
    login_role text NOT NULL,
    authorized boolean NOT NULL,
    mode text NOT NULL,
    written_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_owner_writes_unauthorized_idx
    ON app_owner_writes (written_at) WHERE NOT authorized;

-- Default privileges give faas_runtime DML on every new table; compute must
-- not be able to switch the mode off or erase the record. The converge's
-- runtime-roles.sql repeats this.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON app_owner_authority FROM faas_runtime;
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON app_owner_writes FROM faas_runtime;
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- SECURITY DEFINER so the audit insert works for logins that cannot write
-- app_owner_writes; session_user is still the login that ran the statement
-- (current_user is the definer here, and SET ROLE cannot reach a role the
-- login is not a member of). The tables are qualified with the trigger
-- table's own schema and pg_temp is searched last, so a session cannot shadow
-- app_owner_authority with a temporary table that says 'any'.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_owner_authority_check() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    current_mode text;
    login text := session_user;
    is_authorized boolean;
    previous uuid;
BEGIN
    IF NEW.node_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        previous := OLD.node_id;
        IF NEW.node_id IS NOT DISTINCT FROM previous THEN
            RETURN NEW;
        END IF;
    END IF;
    EXECUTE format('SELECT mode FROM %I.app_owner_authority WHERE singleton', TG_TABLE_SCHEMA)
        INTO current_mode;
    IF current_mode IS NULL OR current_mode = 'any' THEN
        RETURN NEW;
    END IF;
    is_authorized := to_regrole('faas_owner_authority') IS NOT NULL
        AND pg_has_role(login, 'faas_owner_authority', 'MEMBER');
    IF NOT is_authorized AND current_mode = 'enforce' THEN
        RAISE LOG 'faas_app_owner_refused app=% from=% to=% login=%', NEW.id, previous, NEW.node_id, login;
        RAISE EXCEPTION 'app owner changes are made by the control plane'
            USING ERRCODE = '42501',
                  DETAIL = format('app=%s to=%s login=%s', NEW.id, NEW.node_id, login);
    END IF;
    EXECUTE format('INSERT INTO %I.app_owner_writes (app_id, from_node_id, to_node_id, login_role, authorized, mode) '
                   'VALUES ($1, $2, $3, $4, $5, $6)', TG_TABLE_SCHEMA)
        USING NEW.id, previous, NEW.node_id, login, is_authorized, current_mode;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS app_owner_authority_check ON apps;
CREATE TRIGGER app_owner_authority_check BEFORE INSERT OR UPDATE OF node_id
    ON apps FOR EACH ROW EXECUTE FUNCTION app_owner_authority_check();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS app_owner_authority_check ON apps;
DROP FUNCTION IF EXISTS app_owner_authority_check();
DROP TABLE IF EXISTS app_owner_writes;
DROP TABLE IF EXISTS app_owner_authority;
