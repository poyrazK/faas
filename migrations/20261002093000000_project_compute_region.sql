-- +goose Up
ALTER TABLE projects ADD COLUMN compute_region text NOT NULL DEFAULT ''
    CHECK (compute_region = '' OR (length(compute_region) BETWEEN 1 AND 63 AND compute_region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'));

-- +goose StatementBegin
CREATE FUNCTION enforce_project_compute_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE accepting boolean;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.compute_region IS DISTINCT FROM OLD.compute_region THEN
            RAISE EXCEPTION 'project compute region is immutable' USING ERRCODE = '23514', CONSTRAINT = 'project_compute_region_immutable';
        END IF;
    ELSIF NEW.compute_region <> '' THEN
        SELECT accepting_new_apps INTO accepting FROM compute_regions WHERE id = NEW.compute_region FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'unknown compute region' USING ERRCODE = '23514', CONSTRAINT = 'compute_region_unknown', DETAIL = NEW.compute_region;
        END IF;
        IF NOT accepting THEN
            RAISE EXCEPTION 'compute region admission paused' USING ERRCODE = '23514', CONSTRAINT = 'compute_region_admission_paused', DETAIL = NEW.compute_region;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER projects_compute_region_guard BEFORE INSERT OR UPDATE OF compute_region ON projects
    FOR EACH ROW EXECUTE FUNCTION enforce_project_compute_region();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_compute_region_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id THEN
            RAISE EXCEPTION 'compute region identity is immutable' USING ERRCODE = '23514', CONSTRAINT = 'compute_region_identity';
        END IF;
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM apps WHERE compute_region = OLD.id) OR
       EXISTS (SELECT 1 FROM projects WHERE compute_region = OLD.id) OR
       EXISTS (SELECT 1 FROM compute_nodes WHERE region = OLD.id) THEN
        RAISE EXCEPTION 'compute region is in use' USING ERRCODE = '23514', CONSTRAINT = 'compute_region_in_use';
    END IF;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM projects WHERE compute_region <> '') THEN
        RAISE EXCEPTION 'cannot remove project compute regions while regional projects remain';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER projects_compute_region_guard ON projects;
DROP FUNCTION enforce_project_compute_region();
ALTER TABLE projects DROP COLUMN compute_region;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_compute_region_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id THEN
            RAISE EXCEPTION 'compute region identity is immutable' USING ERRCODE = '23514', CONSTRAINT = 'compute_region_identity';
        END IF;
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM apps WHERE compute_region = OLD.id) OR
       EXISTS (SELECT 1 FROM compute_nodes WHERE region = OLD.id) THEN
        RAISE EXCEPTION 'compute region is in use' USING ERRCODE = '23514', CONSTRAINT = 'compute_region_in_use';
    END IF;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
