-- +goose Up
ALTER TABLE jobs ADD COLUMN compute_region text NOT NULL DEFAULT ''
    CHECK (compute_region = '' OR (length(compute_region) BETWEEN 1 AND 63 AND compute_region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'));
CREATE INDEX jobs_compute_region_in_use_idx ON jobs (compute_region) WHERE compute_region <> '';

-- +goose StatementBegin
CREATE FUNCTION enforce_job_compute_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE accepting boolean;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.compute_region IS DISTINCT FROM OLD.compute_region THEN
            RAISE EXCEPTION 'job compute region is immutable' USING ERRCODE = '23514', CONSTRAINT = 'jobs_compute_region_immutable';
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
CREATE TRIGGER jobs_compute_region_guard BEFORE INSERT OR UPDATE OF compute_region ON jobs
    FOR EACH ROW EXECUTE FUNCTION enforce_job_compute_region();

-- Jobs have no app owner. Guard their instance identity independently of the
-- app trigger, including direct SQL and future node-assignment paths.
-- +goose StatementBegin
CREATE FUNCTION enforce_job_instance_compute_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE required_region text; node_region text;
BEGIN
    SELECT compute_region INTO required_region FROM jobs WHERE id = NEW.job_id;
    IF coalesce(required_region, '') <> '' THEN
        SELECT region INTO node_region FROM compute_nodes WHERE id = NEW.node_id FOR SHARE;
        IF node_region IS DISTINCT FROM required_region THEN
            RAISE EXCEPTION 'job instance is outside selected compute region' USING ERRCODE = '23514', CONSTRAINT = 'instances_compute_region';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER instances_job_compute_region_guard BEFORE INSERT OR UPDATE OF job_id, node_id ON instances
    FOR EACH ROW EXECUTE FUNCTION enforce_job_instance_compute_region();

-- Keep idle peers stable too: an in-flight chooser may have selected one.
-- Tombstoned jobs retain their contract for historical instances.
-- +goose StatementBegin
CREATE FUNCTION enforce_bound_job_node_region() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.region IS DISTINCT FROM OLD.region AND
       EXISTS (SELECT 1 FROM jobs WHERE compute_region = OLD.region AND compute_region <> '') THEN
        RAISE EXCEPTION 'node region is in use by region-bound jobs' USING ERRCODE = '23514', CONSTRAINT = 'compute_nodes_region_in_use';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER compute_nodes_bound_job_region_guard BEFORE UPDATE OF region ON compute_nodes
    FOR EACH ROW EXECUTE FUNCTION enforce_bound_job_node_region();

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
       EXISTS (SELECT 1 FROM jobs WHERE compute_region = OLD.id) OR
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
    IF EXISTS (SELECT 1 FROM jobs WHERE compute_region <> '') THEN
        RAISE EXCEPTION 'cannot remove job compute regions while regional jobs remain';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER compute_nodes_bound_job_region_guard ON compute_nodes;
DROP FUNCTION enforce_bound_job_node_region();
DROP TRIGGER instances_job_compute_region_guard ON instances;
DROP FUNCTION enforce_job_instance_compute_region();
DROP TRIGGER jobs_compute_region_guard ON jobs;
DROP FUNCTION enforce_job_compute_region();
ALTER TABLE jobs DROP COLUMN compute_region;
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
