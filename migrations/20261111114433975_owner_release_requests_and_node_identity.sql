-- filename: 20261111114433975_owner_release_requests_and_node_identity.sql
-- migration-compat: expand
-- ADR-20261110210000 §4, slice 1b.
--
-- 1. app_owner_release_requests: under control-plane owner authority a
--    compute node cannot move an app it owns, so a pressured node asks the
--    control plane to take a parked app away. One open request per app; the
--    control plane's schedd checks it against apps.node_id, chooses the
--    destination and deletes the request. A request can only make an app
--    leave its current owner, never choose where it goes.
--
-- 2. compute_nodes identity: the control plane maps a caller's mTLS
--    certificate (its node name) to compute_nodes.id. A login that could
--    rename a row could give its own name to the node that owns an app. The
--    trigger admits a change of id or name only from faas_owner_authority
--    once app_owner_authority.mode is enforce, and records nothing in
--    observe (a rename has no app); it logs faas_node_identity_change.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_owner_release_requests (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    from_node_id uuid NOT NULL,
    reason text NOT NULL DEFAULT 'pressure' CHECK (reason IN ('pressure')),
    requested_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_owner_release_requests_requested_idx
    ON app_owner_release_requests (requested_at);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_node_identity_check() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    current_mode text;
    login text := session_user;
    is_authorized boolean;
BEGIN
    IF NEW.id IS NOT DISTINCT FROM OLD.id AND NEW.name IS NOT DISTINCT FROM OLD.name THEN
        RETURN NEW;
    END IF;
    EXECUTE format('SELECT mode FROM %I.app_owner_authority WHERE singleton', TG_TABLE_SCHEMA)
        INTO current_mode;
    IF current_mode IS NULL OR current_mode = 'any' THEN
        RETURN NEW;
    END IF;
    is_authorized := to_regrole('faas_owner_authority') IS NOT NULL
        AND pg_has_role(login, 'faas_owner_authority', 'MEMBER');
    RAISE LOG 'faas_node_identity_change node=% name=%->% authorized=% login=% mode=%',
        OLD.id, OLD.name, NEW.name, is_authorized, login, current_mode;
    IF NOT is_authorized AND current_mode = 'enforce' THEN
        RAISE EXCEPTION 'compute node identities are changed by the control plane'
            USING ERRCODE = '42501',
                  DETAIL = format('node=%s login=%s', OLD.id, login);
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS compute_node_identity_check ON compute_nodes;
CREATE TRIGGER compute_node_identity_check BEFORE UPDATE OF id, name
    ON compute_nodes FOR EACH ROW EXECUTE FUNCTION compute_node_identity_check();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS compute_node_identity_check ON compute_nodes;
DROP FUNCTION IF EXISTS compute_node_identity_check();
DROP TABLE IF EXISTS app_owner_release_requests;
