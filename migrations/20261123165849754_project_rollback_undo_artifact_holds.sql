-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS project_rollback_undo_artifact_holds (
    operation_id uuid NOT NULL REFERENCES project_rollback_operations(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL,
    storage_keys text[] NOT NULL CHECK (cardinality(storage_keys) > 0),
    expires_at timestamptz,
    PRIMARY KEY (operation_id, deployment_id)
);

CREATE INDEX IF NOT EXISTS project_rollback_undo_artifact_keys_idx
    ON project_rollback_undo_artifact_holds USING gin (storage_keys);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_rollback_protects_artifact(key text) RETURNS boolean
LANGUAGE sql VOLATILE AS $$
    SELECT EXISTS (
        SELECT 1 FROM project_rollback_deploy_protections
        WHERE (storage_keys @> ARRAY[key] OR
               (left(key, 5) = 'sigs/' AND right(key, 4) = '.sig'
                AND storage_keys @> ARRAY[substring(key from 6 for greatest(length(key) - 9, 0))]))
          AND expires_at > clock_timestamp()
    ) OR EXISTS (
        SELECT 1 FROM project_rollback_undo_artifact_holds
        WHERE storage_keys @> ARRAY[key]
          AND (expires_at IS NULL OR expires_at > clock_timestamp())
    );
$$;

-- A worker can settle a pause after a restart or while an older binary
-- is still running. Stamp the deadline in the transaction that settles it.
CREATE OR REPLACE FUNCTION finish_project_rollback_undo_artifact_holds() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'blocked' THEN
        UPDATE project_rollback_undo_artifact_holds
        SET expires_at = clock_timestamp() + interval '24 hours'
        WHERE operation_id = NEW.id AND expires_at IS NULL;
    ELSIF NEW.status = 'complete' THEN
        DELETE FROM project_rollback_undo_artifact_holds WHERE operation_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'finish_project_rollback_undo_artifact_holds'
    ) THEN
        CREATE TRIGGER finish_project_rollback_undo_artifact_holds
            AFTER UPDATE OF status ON project_rollback_operations
            FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status)
            EXECUTE FUNCTION finish_project_rollback_undo_artifact_holds();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER finish_project_rollback_undo_artifact_holds ON project_rollback_operations;
DROP FUNCTION finish_project_rollback_undo_artifact_holds();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_rollback_protects_artifact(key text) RETURNS boolean
LANGUAGE sql VOLATILE AS $$
    SELECT EXISTS (
        SELECT 1 FROM project_rollback_deploy_protections
        WHERE (storage_keys @> ARRAY[key] OR
               (left(key, 5) = 'sigs/' AND right(key, 4) = '.sig'
                AND storage_keys @> ARRAY[substring(key from 6 for greatest(length(key) - 9, 0))]))
          AND expires_at > clock_timestamp()
    );
$$;
-- +goose StatementEnd
DROP TABLE project_rollback_undo_artifact_holds;
