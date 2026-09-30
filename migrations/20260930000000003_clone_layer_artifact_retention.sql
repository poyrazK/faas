-- +goose Up
-- ADR-375: deletion becomes durable before touching a backend. An immutable
-- key in deleting/deleted state can never acquire another consumer.
CREATE TABLE layer_artifact_retention (
    storage_key text PRIMARY KEY CHECK (storage_key <> '' AND length(storage_key) <= 4096),
    state text NOT NULL DEFAULT 'retained' CHECK (state IN ('retained', 'deleting', 'deleted')),
    deletion_id uuid,
    deleted_at timestamptz,
    delete_requested_at timestamptz,
    CHECK (state = 'retained' OR delete_requested_at IS NOT NULL),
    CHECK ((state = 'retained' AND deletion_id IS NULL AND deleted_at IS NULL) OR
           (state = 'deleting' AND deletion_id IS NOT NULL AND deleted_at IS NULL) OR
           (state = 'deleted' AND deletion_id IS NOT NULL AND deleted_at IS NOT NULL))
);
CREATE TABLE project_environment_clone_layer_pins (
    operation_id uuid NOT NULL,
    app_id uuid NOT NULL,
    storage_key text NOT NULL REFERENCES layer_artifact_retention(storage_key),
    bytes bigint NOT NULL CHECK (bytes > 0),
    PRIMARY KEY (operation_id, app_id, storage_key),
    FOREIGN KEY (operation_id, app_id) REFERENCES project_environment_clone_workloads(operation_id, app_id) ON DELETE CASCADE
);
CREATE INDEX project_environment_clone_layer_pins_key_idx ON project_environment_clone_layer_pins(storage_key);

-- +goose StatementBegin
CREATE FUNCTION require_retained_layer_artifact(key text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF key IS NULL OR key = '' THEN RETURN; END IF;
    INSERT INTO layer_artifact_retention(storage_key) VALUES (key) ON CONFLICT DO NOTHING;
    PERFORM 1 FROM layer_artifact_retention WHERE storage_key = key AND state = 'retained' FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'layer artifact is retired' USING ERRCODE = '55000', CONSTRAINT = 'layer_artifact_retention_reference_fence';
    END IF;
END;
$$;
CREATE FUNCTION guard_deployment_layer_artifacts() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE key text;
BEGIN
    IF TG_OP = 'INSERT' OR NEW.rootfs_key IS DISTINCT FROM OLD.rootfs_key OR
       (NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN ('pending', 'building', 'imaging', 'snapshotting', 'live')) THEN
        FOR key IN SELECT keys.key FROM (
            SELECT NEW.rootfs_key AS key
            UNION SELECT storage_key FROM deployment_sidecar_layers WHERE deployment_id = NEW.id
        ) keys WHERE coalesce(keys.key, '') <> '' ORDER BY keys.key LOOP
            PERFORM require_retained_layer_artifact(key);
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER deployments_layer_artifact_guard BEFORE INSERT OR UPDATE OF rootfs_key, status ON deployments
FOR EACH ROW EXECUTE FUNCTION guard_deployment_layer_artifacts();
CREATE FUNCTION guard_sidecar_layer_artifact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM require_retained_layer_artifact(NEW.storage_key);
    RETURN NEW;
END;
$$;
CREATE TRIGGER sidecar_layer_artifact_guard BEFORE INSERT OR UPDATE OF storage_key ON deployment_sidecar_layers
FOR EACH ROW EXECUTE FUNCTION guard_sidecar_layer_artifact();

CREATE FUNCTION require_deployment_layer_artifacts(deployment uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE key text;
BEGIN
    FOR key IN SELECT keys.key FROM (
        SELECT rootfs_key AS key FROM deployments WHERE id = deployment
        UNION SELECT storage_key FROM deployment_sidecar_layers WHERE deployment_id = deployment
    ) keys WHERE coalesce(keys.key, '') <> '' ORDER BY keys.key LOOP
        PERFORM require_retained_layer_artifact(key);
    END LOOP;
END;
$$;
CREATE FUNCTION guard_snapshot_layer_artifacts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT NEW.stale THEN
        PERFORM require_deployment_layer_artifacts(NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER snapshot_layer_artifact_guard BEFORE INSERT OR UPDATE OF deployment_id, stale ON snapshots
FOR EACH ROW EXECUTE FUNCTION guard_snapshot_layer_artifacts();
CREATE FUNCTION guard_instance_layer_artifacts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deployment_id IS NOT NULL AND NEW.state IN ('pending', 'waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining') THEN
        PERFORM require_deployment_layer_artifacts(NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER instance_layer_artifact_guard BEFORE INSERT OR UPDATE OF deployment_id, state ON instances
FOR EACH ROW EXECUTE FUNCTION guard_instance_layer_artifacts();
-- +goose StatementEnd

-- Existing private captures must acquire the same retention as new captures.
WITH keys AS (
    SELECT w.operation_id, w.app_id, w.snapshot::jsonb #>> '{artifact,rootfs_key}' AS storage_key,
           (w.snapshot::jsonb #>> '{artifact,rootfs_bytes}')::bigint AS bytes
    FROM project_environment_clone_workloads w
    UNION ALL
    SELECT w.operation_id, w.app_id, layer->>'storage_key', (layer->>'bytes')::bigint
    FROM project_environment_clone_workloads w CROSS JOIN LATERAL jsonb_array_elements(w.snapshot::jsonb->'layers') layer
)
INSERT INTO layer_artifact_retention(storage_key)
SELECT DISTINCT storage_key FROM keys WHERE coalesce(storage_key, '') <> '' AND bytes > 0 ON CONFLICT DO NOTHING;

WITH keys AS (
    SELECT w.operation_id, w.app_id, w.snapshot::jsonb #>> '{artifact,rootfs_key}' AS storage_key,
           (w.snapshot::jsonb #>> '{artifact,rootfs_bytes}')::bigint AS bytes
    FROM project_environment_clone_workloads w
    UNION ALL
    SELECT w.operation_id, w.app_id, layer->>'storage_key', (layer->>'bytes')::bigint
    FROM project_environment_clone_workloads w CROSS JOIN LATERAL jsonb_array_elements(w.snapshot::jsonb->'layers') layer
)
INSERT INTO project_environment_clone_layer_pins(operation_id, app_id, storage_key, bytes)
SELECT operation_id, app_id, storage_key, max(bytes) FROM keys
WHERE coalesce(storage_key, '') <> '' AND bytes > 0 GROUP BY operation_id, app_id, storage_key;

-- +goose Down
DROP TRIGGER instance_layer_artifact_guard ON instances;
DROP FUNCTION guard_instance_layer_artifacts();
DROP TRIGGER snapshot_layer_artifact_guard ON snapshots;
DROP FUNCTION guard_snapshot_layer_artifacts();
DROP FUNCTION require_deployment_layer_artifacts(uuid);
DROP TRIGGER sidecar_layer_artifact_guard ON deployment_sidecar_layers;
DROP FUNCTION guard_sidecar_layer_artifact();
DROP TRIGGER deployments_layer_artifact_guard ON deployments;
DROP FUNCTION guard_deployment_layer_artifacts();
DROP FUNCTION require_retained_layer_artifact(text);
DROP TABLE project_environment_clone_layer_pins;
DROP TABLE layer_artifact_retention;
