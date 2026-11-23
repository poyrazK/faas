-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE project_rollback_deploy_protections (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deploy_group_id uuid NOT NULL,
    manifest_hash text NOT NULL,
    deployment_ids text[] NOT NULL,
    storage_keys text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    layer_bytes bigint NOT NULL CHECK (layer_bytes > 0),
    PRIMARY KEY (project_id, deploy_group_id, manifest_hash)
);

CREATE INDEX project_rollback_protection_keys_idx
    ON project_rollback_deploy_protections USING gin (storage_keys);

-- +goose StatementBegin
CREATE FUNCTION project_rollback_protects_artifact(key text) RETURNS boolean
LANGUAGE sql VOLATILE AS $$
    SELECT EXISTS (
        SELECT 1 FROM project_rollback_deploy_protections
        WHERE (storage_keys @> ARRAY[key] OR
               (left(key, 5) = 'sigs/' AND right(key, 4) = '.sig'
                AND storage_keys @> ARRAY[substring(key from 6 for greatest(length(key) - 9, 0))]))
          AND expires_at > clock_timestamp()
    );
$$;

-- Protect against older workers which claim directly after their reference check.
CREATE FUNCTION guard_project_rollback_artifact_deletion() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF project_rollback_protects_artifact(OLD.storage_key) THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER guard_project_rollback_artifact_deletion
    BEFORE UPDATE OF state ON layer_artifact_retention
    FOR EACH ROW WHEN (OLD.state = 'retained' AND NEW.state = 'deleting')
    EXECUTE FUNCTION guard_project_rollback_artifact_deletion();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER guard_project_rollback_artifact_deletion ON layer_artifact_retention;
DROP FUNCTION guard_project_rollback_artifact_deletion();
DROP FUNCTION project_rollback_protects_artifact(text);
DROP TABLE project_rollback_deploy_protections;
