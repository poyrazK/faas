-- filename: 20261029083739103_compute_release_activations.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Issue #574 follow-up: compute hosts in the release history. deployctl
-- records control-plane activations; a compute host changes release through
-- `gregalectl release install`, which stamps compute_nodes.release_id after
-- the release's binaries are live (the node may still be drained for
-- readiness). Each change of that column becomes a finished deploy row for
-- the node, with the release it replaced.
--
-- The installer logs in as a runtime role (faas_compute), whose writes to the
-- history are revoked so no daemon can forge or rewrite it. The function is
-- SECURITY DEFINER: it runs as the schema owner, but writes only what the
-- compute_nodes change says. It resolves the history table in the trigger's
-- own schema with a fixed search_path.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_compute_release_activation() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog
    AS $$
BEGIN
    EXECUTE format(
        'INSERT INTO %I.platform_release_activations
             (host, kind, release_id, commit_sha, previous_release_id, actor, status, detail, started_at, finished_at)
         VALUES ($1, ''deploy'', $2, $3, $4, $5, ''succeeded'', ''release install stamped compute_nodes.release_id'', now(), now())',
        TG_TABLE_SCHEMA)
    USING left(NEW.name, 253),
          left(NEW.release_id, 128),
          CASE WHEN NEW.release_id ~ '^[0-9a-f]{40}$' THEN NEW.release_id END,
          CASE WHEN TG_OP = 'UPDATE' THEN left(NULLIF(OLD.release_id, ''), 128) END,
          left('release install as ' || session_user, 200);
    RETURN NULL;
EXCEPTION WHEN OTHERS THEN
    -- The history is best effort, as deployctl's is: never fail the install.
    RAISE WARNING 'release history: record % on %: %', NEW.release_id, NEW.name, SQLERRM;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
-- No GRANT or REVOKE on the function: PostgreSQL only runs a trigger function
-- as a trigger, and ledger recovery (CheckMigrationRecoveryWriters) admits
-- functions only with the default ACL.

DROP TRIGGER IF EXISTS compute_nodes_release_activation_insert ON compute_nodes;
CREATE TRIGGER compute_nodes_release_activation_insert
    AFTER INSERT ON compute_nodes
    FOR EACH ROW
    WHEN (NEW.release_id IS NOT NULL AND NEW.release_id <> '')
    EXECUTE FUNCTION record_compute_release_activation();

DROP TRIGGER IF EXISTS compute_nodes_release_activation_update ON compute_nodes;
CREATE TRIGGER compute_nodes_release_activation_update
    AFTER UPDATE OF release_id ON compute_nodes
    FOR EACH ROW
    WHEN (NEW.release_id IS NOT NULL AND NEW.release_id <> '' AND OLD.release_id IS DISTINCT FROM NEW.release_id)
    EXECUTE FUNCTION record_compute_release_activation();

COMMENT ON TABLE platform_release_activations IS
    'Release activations: control-plane deploys and rollbacks written by deployctl as the schema owner, and compute release installs recorded from compute_nodes.release_id. Read with gregalectl release history (issue #574).';

-- +goose Down
COMMENT ON TABLE platform_release_activations IS
    'Release activations (deploy, rollback) per control-plane host, written by deployctl as the schema owner. Read with gregalectl release history (issue #574).';
DROP TRIGGER IF EXISTS compute_nodes_release_activation_update ON compute_nodes;
DROP TRIGGER IF EXISTS compute_nodes_release_activation_insert ON compute_nodes;
DROP FUNCTION IF EXISTS record_compute_release_activation();
