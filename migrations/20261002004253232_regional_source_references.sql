-- filename: 20261002004253232_regional_source_references.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS source_storage_key text NOT NULL DEFAULT '';
DO $guard$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployments'::regclass AND conname = 'deployments_source_storage_key_shape') THEN
        ALTER TABLE deployments ADD CONSTRAINT deployments_source_storage_key_shape CHECK (
            source_storage_key = '' OR source_storage_key ~
            '^(regions/[a-z0-9]([a-z0-9-]*[a-z0-9])?/)?sources/([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})[.]tar[.]gz$'
        );
    END IF;
END $guard$;

CREATE INDEX IF NOT EXISTS deployments_pending_source_reference ON deployments (source_storage_key)
    WHERE source_storage_key <> '' AND status IN ('pending', 'building');

CREATE OR REPLACE FUNCTION enforce_deployment_source_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    selected_region text;
    source_region text;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.source_storage_key IS DISTINCT FROM OLD.source_storage_key THEN
        RAISE EXCEPTION 'deployment source reference is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'deployments_source_reference_immutable';
    END IF;
    IF NEW.source_storage_key <> '' THEN
        SELECT compute_region INTO selected_region FROM apps WHERE id = NEW.app_id FOR SHARE;
        source_region := CASE WHEN NEW.source_storage_key LIKE 'regions/%'
            THEN split_part(NEW.source_storage_key, '/', 2) ELSE '' END;
        IF source_region IS DISTINCT FROM selected_region THEN
            RAISE EXCEPTION 'source reference must match app compute region'
                USING ERRCODE = '23514', CONSTRAINT = 'deployments_source_compute_region';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS deployments_source_region ON deployments;
CREATE TRIGGER deployments_source_region BEFORE INSERT OR UPDATE OF source_storage_key, app_id
    ON deployments FOR EACH ROW EXECUTE FUNCTION enforce_deployment_source_region();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER deployments_source_region ON deployments;
DROP FUNCTION enforce_deployment_source_region();
DROP INDEX deployments_pending_source_reference;
ALTER TABLE deployments DROP COLUMN source_storage_key;
-- +goose StatementEnd
