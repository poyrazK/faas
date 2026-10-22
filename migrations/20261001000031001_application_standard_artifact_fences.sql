-- +goose Up
-- Artifact-child mutations often begin with an instance/sidecar row lock.
-- Making them wait on an app/deployment row introduces a reverse edge into
-- legacy app-first lifecycle and deletion transactions. Approval never locks
-- those children: a dedicated shared advisory fence protects their snapshot
-- membership without adding another parent-row dependency to old writers.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_artifact_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE deployment_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN deployment_ids := array_append(deployment_ids, OLD.deployment_id); END IF;
    IF TG_OP <> 'DELETE' THEN deployment_ids := array_append(deployment_ids, NEW.deployment_id); END IF;
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.' || id::text, 0))
    FROM (SELECT DISTINCT id FROM unnest(deployment_ids) id WHERE id IS NOT NULL ORDER BY id) artifacts;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Existing approval locks every deployment of the reviewed apps directly;
-- metadata updates/deletion already serialize on those rows. Deployment
-- insertion has its existing app admission guard. A retained artifact cannot
-- be moved behind that lock set; clones create a distinct deployment instead.
DROP TRIGGER application_standard_artifact_input_guard ON deployments;
-- +goose StatementBegin
CREATE FUNCTION application_standard_artifact_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'deployment application identity is immutable'
        USING ERRCODE = '23514', CONSTRAINT = 'application_standard_artifact_identity';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_artifact_identity_guard
BEFORE UPDATE OF app_id ON deployments FOR EACH ROW
WHEN (OLD.app_id IS DISTINCT FROM NEW.app_id)
EXECUTE FUNCTION application_standard_artifact_identity_guard();

-- +goose Down
DROP TRIGGER application_standard_artifact_identity_guard ON deployments;
DROP FUNCTION application_standard_artifact_identity_guard();
CREATE TRIGGER application_standard_artifact_input_guard
BEFORE INSERT OR DELETE OR UPDATE OF app_id, scope, kind, status, image_digest, rootfs_key,
    rootfs_bytes, source_sha256, parked_reason, scan_status, scan_result ON deployments
FOR EACH ROW EXECUTE FUNCTION application_standard_control_input_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_artifact_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE deployment_ids uuid[] := '{}'; app_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN deployment_ids := array_append(deployment_ids, OLD.deployment_id); END IF;
    IF TG_OP <> 'DELETE' THEN deployment_ids := array_append(deployment_ids, NEW.deployment_id); END IF;
    IF TG_TABLE_NAME = 'instances' THEN
        IF TG_OP <> 'INSERT' THEN app_ids := array_append(app_ids, OLD.app_id); END IF;
        IF TG_OP <> 'DELETE' THEN app_ids := array_append(app_ids, NEW.app_id); END IF;
    END IF;
    PERFORM 1 FROM apps WHERE id = ANY(app_ids) OR id IN (SELECT app_id FROM deployments WHERE id = ANY(deployment_ids))
    ORDER BY id FOR SHARE;
    PERFORM 1 FROM deployments WHERE id = ANY(deployment_ids) ORDER BY id FOR SHARE;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
