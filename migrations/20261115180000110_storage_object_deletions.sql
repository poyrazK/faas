-- filename: 20261115180000110_storage_object_deletions.sql
-- migration-compat: expand
-- ADR-20261115180000: object deletion moves to the control plane.

-- +goose Up
--
-- A compute node in FAAS_STORAGE_DELETE_MODE=defer records the shared-store
-- objects it would have deleted here instead of deleting them, so its store
-- credentials need no delete right. The control plane's object GC
-- (FAAS_SCHEDD_OBJECT_GC) re-checks every request against the database and
-- deletes only objects nothing references; a request for a live object
-- is retried, then refused and kept as evidence.
--
--   * One row per key. A new request for a key resets the row to requested
--     and is first tried 10 minutes later (next_attempt_at), so the rows of
--     a cleanup that deletes files before rows are gone by then. A key still
--     referenced is retried with backoff and refused after 7 days.
--   * requested_by is the requesting login, and the node's name for a
--     per-node compute login (compute_node_db_logins).
--   * A per-node compute login only requests: it cannot record an outcome,
--     bring its next attempt forward, or remove rows.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS storage_object_deletions (
    storage_key  text PRIMARY KEY
        CHECK (length(storage_key) BETWEEN 1 AND 1024 AND storage_key !~ '(^/|\.\.|//)'),
    reason       text NOT NULL DEFAULT '' CHECK (length(reason) <= 200),
    requested_by text NOT NULL DEFAULT '' CHECK (length(requested_by) <= 253),
    requested_at timestamp with time zone NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(requested_at)),
    state        text NOT NULL DEFAULT 'requested' CHECK (state IN ('requested', 'deleted', 'refused')),
    next_attempt_at timestamp with time zone NOT NULL DEFAULT clock_timestamp() + interval '10 minutes'
        CHECK (isfinite(next_attempt_at)),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error   text NOT NULL DEFAULT '' CHECK (length(last_error) <= 1000),
    decided_at   timestamp with time zone CHECK (decided_at IS NULL OR isfinite(decided_at)),
    CHECK ((state = 'requested') = (decided_at IS NULL))
);
CREATE INDEX IF NOT EXISTS storage_object_deletions_due_idx
    ON storage_object_deletions (next_attempt_at) WHERE state = 'requested';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION storage_object_deletions_scope() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    node text;
BEGIN
    EXECUTE format('SELECT node_name FROM %I.compute_node_db_logins WHERE login = $1', TG_TABLE_SCHEMA)
        INTO node USING session_user::text;
    IF node IS NULL THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        IF TG_OP = 'INSERT' AND NEW.requested_by = '' THEN
            NEW.requested_by := session_user::text;
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'a compute node login cannot remove object deletion requests'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s', session_user, node);
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.storage_key IS DISTINCT FROM OLD.storage_key THEN
        RAISE EXCEPTION 'a compute node login cannot rename an object deletion request'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s', session_user, node);
    END IF;
    -- Whatever the node sent, its row is a fresh request.
    NEW.requested_by := node;
    NEW.requested_at := clock_timestamp();
    NEW.next_attempt_at := clock_timestamp() + interval '10 minutes';
    NEW.state := 'requested';
    NEW.attempts := 0;
    NEW.last_error := '';
    NEW.decided_at := NULL;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS storage_object_deletions_scope ON storage_object_deletions;
CREATE TRIGGER storage_object_deletions_scope BEFORE INSERT OR UPDATE OR DELETE
    ON storage_object_deletions FOR EACH ROW EXECUTE FUNCTION storage_object_deletions_scope();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS storage_object_deletions_scope ON storage_object_deletions;
DROP FUNCTION IF EXISTS storage_object_deletions_scope();
DROP TABLE IF EXISTS storage_object_deletions;
