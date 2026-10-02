-- filename: 20261002013759246_regional_build_exports.sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS build_artifact_key text NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS build_artifact_sha256 text NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS build_artifact_bytes bigint NOT NULL DEFAULT 0;
DO $guard$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployments'::regclass AND conname = 'deployments_build_artifact_shape') THEN
        ALTER TABLE deployments ADD CONSTRAINT deployments_build_artifact_shape CHECK (
            (build_artifact_key = '' AND build_artifact_sha256 = '' AND build_artifact_bytes = 0)
            OR (build_artifact_bytes > 0 AND build_artifact_sha256 ~ '^[0-9a-f]{64}$'
                AND build_artifact_key ~ ('^(regions/[a-z0-9]([a-z0-9-]*[a-z0-9])?/)?build-exports/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/' || build_artifact_sha256 || '[.]oci[.]tar$'))
        );
    END IF;
END $guard$;
CREATE INDEX IF NOT EXISTS deployments_active_build_artifact ON deployments (build_artifact_key)
    WHERE build_artifact_key <> '' AND status IN ('pending', 'building', 'imaging', 'snapshotting');

CREATE OR REPLACE FUNCTION enforce_deployment_build_artifact() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected_region text; artifact_region text;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.build_artifact_key <> ''
       AND (NEW.build_artifact_key, NEW.build_artifact_sha256, NEW.build_artifact_bytes, NEW.app_id)
           IS DISTINCT FROM (OLD.build_artifact_key, OLD.build_artifact_sha256, OLD.build_artifact_bytes, OLD.app_id) THEN
        RAISE EXCEPTION 'completed build artifact is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'deployments_build_artifact_immutable';
    END IF;
    IF NEW.build_artifact_key <> '' THEN
        SELECT compute_region INTO selected_region FROM apps WHERE id = NEW.app_id FOR SHARE;
        artifact_region := CASE WHEN NEW.build_artifact_key LIKE 'regions/%'
            THEN split_part(NEW.build_artifact_key, '/', 2) ELSE '' END;
        IF artifact_region IS DISTINCT FROM selected_region THEN
            RAISE EXCEPTION 'build artifact must match app compute region'
                USING ERRCODE = '23514', CONSTRAINT = 'deployments_build_artifact_region';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS deployments_build_artifact ON deployments;
CREATE TRIGGER deployments_build_artifact BEFORE INSERT OR UPDATE OF
    build_artifact_key, build_artifact_sha256, build_artifact_bytes, app_id
    ON deployments FOR EACH ROW EXECUTE FUNCTION enforce_deployment_build_artifact();

CREATE OR REPLACE FUNCTION enforce_regional_build_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'succeeded' AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM NEW.status)
       AND EXISTS (SELECT 1 FROM deployments d JOIN apps a ON a.id = d.app_id
                   WHERE d.id = NEW.deployment_id AND a.compute_region <> '' AND d.build_artifact_key = '') THEN
        RAISE EXCEPTION 'regional build success requires a durable export'
            USING ERRCODE = '23514', CONSTRAINT = 'builds_regional_export_required';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS builds_regional_completion ON builds;
CREATE TRIGGER builds_regional_completion BEFORE INSERT OR UPDATE OF status
    ON builds FOR EACH ROW EXECUTE FUNCTION enforce_regional_build_completion();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $guard$ BEGIN
    IF EXISTS (SELECT 1 FROM deployments WHERE build_artifact_key LIKE 'regions/%') THEN
        RAISE EXCEPTION 'cannot remove durable regional build exports';
    END IF;
END $guard$;
DROP TRIGGER builds_regional_completion ON builds;
DROP FUNCTION enforce_regional_build_completion();
DROP TRIGGER deployments_build_artifact ON deployments;
DROP FUNCTION enforce_deployment_build_artifact();
DROP INDEX deployments_active_build_artifact;
ALTER TABLE deployments DROP CONSTRAINT deployments_build_artifact_shape;
ALTER TABLE deployments DROP COLUMN build_artifact_key, DROP COLUMN build_artifact_sha256, DROP COLUMN build_artifact_bytes;
-- +goose StatementEnd
