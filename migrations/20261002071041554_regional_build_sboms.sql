-- filename: 20261002071041554_regional_build_sboms.sql
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_build_sbom_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected_region text; expected_key text;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.build_id IS NOT DISTINCT FROM OLD.build_id
       AND NEW.sbom_storage_key IS NOT DISTINCT FROM OLD.sbom_storage_key THEN
        RETURN NEW; -- Historical references survive unrelated provenance updates.
    END IF;
    IF coalesce(NEW.sbom_storage_key, '') = '' THEN
        RETURN NEW;
    END IF;
    SELECT a.compute_region INTO selected_region
      FROM builds b JOIN deployments d ON d.id = b.deployment_id JOIN apps a ON a.id = d.app_id
     WHERE b.id = NEW.build_id FOR SHARE OF b, d, a;
    expected_key := CASE WHEN selected_region = '' THEN '' ELSE 'regions/' || selected_region || '/' END
                    || 'sboms/' || NEW.build_id::text || '.cdx.json';
    IF (selected_region <> '' OR NEW.sbom_storage_key ~ '^regions(/|$)')
       AND NEW.sbom_storage_key IS DISTINCT FROM expected_key THEN
        RAISE EXCEPTION 'SBOM must match its build and app compute region'
            USING ERRCODE = '23514', CONSTRAINT = 'build_provenance_sbom_region';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS build_provenance_sbom_region ON build_provenance;
CREATE TRIGGER build_provenance_sbom_region BEFORE INSERT OR UPDATE OF build_id, sbom_storage_key
    ON build_provenance FOR EACH ROW EXECUTE FUNCTION enforce_build_sbom_region();

CREATE OR REPLACE FUNCTION enforce_regional_sbom_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'builds' THEN
        IF NEW.deployment_id IS DISTINCT FROM OLD.deployment_id AND EXISTS (
            SELECT 1 FROM build_provenance p WHERE p.build_id = OLD.id AND p.sbom_storage_key LIKE 'regions/%'
        ) THEN
            RAISE EXCEPTION 'regional SBOM build identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'builds_sbom_identity_immutable';
        END IF;
    ELSE
        IF NEW.app_id IS DISTINCT FROM OLD.app_id AND EXISTS (
            SELECT 1 FROM builds b JOIN build_provenance p ON p.build_id = b.id
             WHERE b.deployment_id = OLD.id AND p.sbom_storage_key LIKE 'regions/%'
        ) THEN
            RAISE EXCEPTION 'regional SBOM app identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'deployments_sbom_identity_immutable';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS builds_sbom_identity ON builds;
CREATE TRIGGER builds_sbom_identity BEFORE UPDATE OF deployment_id ON builds
    FOR EACH ROW EXECUTE FUNCTION enforce_regional_sbom_identity();
DROP TRIGGER IF EXISTS deployments_sbom_identity ON deployments;
CREATE TRIGGER deployments_sbom_identity BEFORE UPDATE OF app_id ON deployments
    FOR EACH ROW EXECUTE FUNCTION enforce_regional_sbom_identity();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $guard$ BEGIN
    IF EXISTS (SELECT 1 FROM build_provenance WHERE sbom_storage_key LIKE 'regions/%') THEN
        RAISE EXCEPTION 'cannot remove regional SBOM reference guards';
    END IF;
END $guard$;
DROP TRIGGER deployments_sbom_identity ON deployments;
DROP TRIGGER builds_sbom_identity ON builds;
DROP FUNCTION enforce_regional_sbom_identity();
DROP TRIGGER build_provenance_sbom_region ON build_provenance;
DROP FUNCTION enforce_build_sbom_region();
-- +goose StatementEnd
