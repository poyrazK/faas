-- +goose Up
CREATE TABLE compute_regions (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 63 AND id ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128 AND btrim(display_name) = display_name AND display_name !~ '[[:cntrl:]]'),
    accepting_new_apps boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Include inactive nodes and app tombstones: an outage must not erase a region.
INSERT INTO compute_regions (id, display_name)
SELECT region, region FROM (
    SELECT region FROM compute_nodes UNION SELECT compute_region FROM apps
) labels
WHERE length(region) BETWEEN 1 AND 63 AND region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$';

-- A policy update and app admission serialize on the registry row. Only INSERT
-- checks admission; existing apps can wake, recover and restore while paused.
-- +goose StatementBegin
CREATE FUNCTION enforce_new_app_compute_region_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE accepting boolean;
BEGIN
    IF NEW.compute_region <> '' THEN
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
CREATE TRIGGER apps_compute_region_admission_guard BEFORE INSERT ON apps
    FOR EACH ROW EXECUTE FUNCTION enforce_new_app_compute_region_admission();

-- +goose StatementBegin
CREATE FUNCTION protect_compute_region_identity() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER compute_regions_identity_guard BEFORE UPDATE OF id OR DELETE ON compute_regions
    FOR EACH ROW EXECUTE FUNCTION protect_compute_region_identity();

-- +goose Down
-- Retain a paused admission policy rather than silently reopening it on rollback.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM compute_regions WHERE NOT accepting_new_apps) THEN
        RAISE EXCEPTION 'cannot remove compute region registry while admission is paused';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER apps_compute_region_admission_guard ON apps;
DROP FUNCTION enforce_new_app_compute_region_admission();
DROP TRIGGER compute_regions_identity_guard ON compute_regions;
DROP FUNCTION protect_compute_region_identity();
DROP TABLE compute_regions;
