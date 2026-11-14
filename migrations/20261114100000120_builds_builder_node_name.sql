-- filename: 20261114100000120_builds_builder_node_name.sql
-- migration-compat: expand
-- ADR-20261110210000 slice 3j-3: the node that runs a build.

-- +goose Up
--
-- Any compute node's builderd may claim a queued build, and nothing recorded
-- which one until build_provenance at the end. secretd now pulls an app's
-- private base images for a running build's builder VM (the registry mirror
-- vmmd serves it), and admits only the node that runs the build, so the
-- claim records that node in builds.builder_node_name. The claim functions
-- do not change: a trigger stamps the node when a build moves from queued to
-- running.
--
--   * A per-node login (compute_node_db_logins, slice 1c) stamps its own
--     node, and cannot otherwise set or change the column: it cannot point
--     another node's running build at itself.
--   * Any other login stamps nothing; builderd then records its node on the
--     running build itself (builder_node_name IS NULL). That is no weaker
--     than the shared logins' trust today; slice 1c-3 retires them.
--   * A build that leaves running, or is inserted, carries no node.
SET LOCAL lock_timeout = '5s';

ALTER TABLE builds ADD COLUMN IF NOT EXISTS builder_node_name text;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'builds_builder_node_name_check') THEN
        ALTER TABLE builds ADD CONSTRAINT builds_builder_node_name_check
            CHECK (builder_node_name IS NULL OR builder_node_name ~ '^[A-Za-z0-9._-]{1,253}$') NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION builds_builder_node_scope() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    node text;
BEGIN
    -- Only a running build names a node; an insert never does.
    IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM 'running' THEN
        NEW.builder_node_name := NULL;
        RETURN NEW;
    END IF;
    -- The login map is read only for a claim or a change of the node, not
    -- for the many other updates of a running build.
    IF OLD.status IS DISTINCT FROM 'queued' AND NEW.builder_node_name IS NOT DISTINCT FROM OLD.builder_node_name THEN
        RETURN NEW;
    END IF;
    EXECUTE format('SELECT node_name FROM %I.compute_node_db_logins WHERE login = $1', TG_TABLE_SCHEMA)
        INTO node USING session_user::text;
    IF OLD.status = 'queued' THEN
        NEW.builder_node_name := node;
        RETURN NEW;
    END IF;
    IF node IS NOT NULL THEN
        RAISE EXCEPTION 'a compute node login records only its own claim of a build'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s build=%s', session_user, node, OLD.id);
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS builds_builder_node_scope ON builds;
CREATE TRIGGER builds_builder_node_scope BEFORE INSERT OR UPDATE
    ON builds FOR EACH ROW EXECUTE FUNCTION builds_builder_node_scope();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS builds_builder_node_scope ON builds;
DROP FUNCTION IF EXISTS builds_builder_node_scope();
ALTER TABLE builds DROP CONSTRAINT IF EXISTS builds_builder_node_name_check;
ALTER TABLE builds DROP COLUMN IF EXISTS builder_node_name;
