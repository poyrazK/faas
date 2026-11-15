-- filename: 20261115170000110_snapshots_sealed_key.sql
-- migration-compat: expand
-- ADR-20261110210000 slice 3d-3: park and warm snapshots sealed at the source.

-- +goose Up
--
-- With FAAS_SCHEDD_SEAL_SNAPSHOTS on, vmmd publishes a park or warm capture
-- only as age-encrypted twins (ADR-733 sealed captures) and returns the
-- capture key sealed to the capture recipient. imaged records that key on
-- the snapshot row; a wake hands it to vmmd, which opens it through secretd
-- by the capture id. A node's object-store credentials then neither read
-- another app's snapshot nor rewrite it undetected: age is authenticated.
--
--   * sealed_key and sealed_capture_id are both set or both NULL, and the id
--     is the capture token of storage_key (the id the key is bound to).
--   * They are written with the row and never change.
--   * A per-node compute login (compute_node_db_logins) records snapshot
--     rows only for deployments with an instance on its node: sealing needs
--     only the public recipient, so without this a node could record a
--     snapshot of its own making for any app.
SET LOCAL lock_timeout = '5s';

ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS sealed_key bytea;
ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS sealed_capture_id uuid;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'snapshots_sealed_key_check') THEN
        ALTER TABLE snapshots ADD CONSTRAINT snapshots_sealed_key_check CHECK (
            (sealed_key IS NULL AND sealed_capture_id IS NULL)
            OR (sealed_key IS NOT NULL AND sealed_capture_id IS NOT NULL
                AND octet_length(sealed_key) BETWEEN 1 AND 4096
                AND storage_key LIKE '%/captures/' || sealed_capture_id::text || '/v2/mem')) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION snapshots_sealed_key_guard() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    node_name text;
    node uuid;
    ran boolean;
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.sealed_key IS DISTINCT FROM OLD.sealed_key
        OR NEW.sealed_capture_id IS DISTINCT FROM OLD.sealed_capture_id) THEN
        RAISE EXCEPTION 'a snapshot''s sealed key is recorded with the row and never changes'
            USING ERRCODE = '42501', DETAIL = format('login=%s snapshot=%s', session_user, OLD.id);
    END IF;
    EXECUTE format('SELECT node_name FROM %I.compute_node_db_logins WHERE login = $1', TG_TABLE_SCHEMA)
        INTO node_name USING session_user::text;
    IF node_name IS NULL THEN
        RETURN NEW;
    END IF;
    EXECUTE format('SELECT id FROM %I.compute_nodes WHERE name = $1', TG_TABLE_SCHEMA)
        INTO node USING node_name;
    EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I.instances WHERE deployment_id = $1 AND node_id = $2)', TG_TABLE_SCHEMA)
        INTO ran USING NEW.deployment_id, node;
    IF NOT coalesce(ran, false) THEN
        RAISE EXCEPTION 'a compute node login records snapshots only of deployments that ran on its node'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s deployment=%s', session_user, node_name, NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS snapshots_sealed_key_guard ON snapshots;
CREATE TRIGGER snapshots_sealed_key_guard BEFORE INSERT OR UPDATE OF deployment_id, sealed_key, sealed_capture_id
    ON snapshots FOR EACH ROW EXECUTE FUNCTION snapshots_sealed_key_guard();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS snapshots_sealed_key_guard ON snapshots;
DROP FUNCTION IF EXISTS snapshots_sealed_key_guard();
ALTER TABLE snapshots DROP CONSTRAINT IF EXISTS snapshots_sealed_key_check;
ALTER TABLE snapshots DROP COLUMN IF EXISTS sealed_capture_id;
ALTER TABLE snapshots DROP COLUMN IF EXISTS sealed_key;
