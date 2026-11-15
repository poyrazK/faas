-- filename: 20261115160000410_deployments_build_registry_scope.sql
-- migration-compat: expand
-- ADR-20261110210000 slice 3j-4: the registry repositories a deployment's
-- builds may pull with the app's credential.

-- +goose Up
--
-- apid records, when it creates a source deployment, the image repositories
-- its Dockerfiles name (FROM, COPY --from, RUN --mount from=). secretd lets
-- that deployment's builds pull only those repositories through the
-- registry mirror (slice 3j-3), so a node that claims another app's build
-- gets at most that build's own base images, never the rest of what the
-- app's credential reaches. NULL means not recorded.
--
--   * The column is set when the row is inserted and never changes.
--   * A per-node compute login (compute_node_db_logins) inserts it as NULL:
--     a node cannot create a deployment that widens its own scope.
SET LOCAL lock_timeout = '5s';

ALTER TABLE deployments ADD COLUMN IF NOT EXISTS build_registry_scope text[];

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION deployment_build_registry_scope_valid(scope text[]) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    SET search_path = pg_catalog, pg_temp
    AS $function$
    SELECT scope IS NULL OR (
        cardinality(scope) <= 64
        AND array_position(scope, NULL) IS NULL
        AND NOT EXISTS (
            SELECT 1 FROM unnest(scope) AS e
            WHERE length(e) > 255 OR e !~ '^[a-z0-9.-]+(:[0-9]{1,5})?/[a-z0-9._/-]+$'))
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deployments_build_registry_scope_check') THEN
        ALTER TABLE deployments ADD CONSTRAINT deployments_build_registry_scope_check
            CHECK (deployment_build_registry_scope_valid(build_registry_scope)) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION deployments_build_registry_scope_guard() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    node text;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.build_registry_scope IS DISTINCT FROM OLD.build_registry_scope THEN
            RAISE EXCEPTION 'a deployment''s build registry scope is recorded when it is created and never changes'
                USING ERRCODE = '42501', DETAIL = format('login=%s deployment=%s', session_user, OLD.id);
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.build_registry_scope IS NULL THEN
        RETURN NEW;
    END IF;
    EXECUTE format('SELECT node_name FROM %I.compute_node_db_logins WHERE login = $1', TG_TABLE_SCHEMA)
        INTO node USING session_user::text;
    IF node IS NOT NULL THEN
        NEW.build_registry_scope := NULL;
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS deployments_build_registry_scope_guard ON deployments;
CREATE TRIGGER deployments_build_registry_scope_guard BEFORE INSERT OR UPDATE OF build_registry_scope
    ON deployments FOR EACH ROW EXECUTE FUNCTION deployments_build_registry_scope_guard();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS deployments_build_registry_scope_guard ON deployments;
DROP FUNCTION IF EXISTS deployments_build_registry_scope_guard();
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_build_registry_scope_check;
ALTER TABLE deployments DROP COLUMN IF EXISTS build_registry_scope;
DROP FUNCTION IF EXISTS deployment_build_registry_scope_valid(text[]);
