-- +goose Up
ALTER TABLE apps ADD COLUMN compute_region text NOT NULL DEFAULT ''
    CONSTRAINT apps_compute_region_chk CHECK (
        compute_region = '' OR
        (length(compute_region) <= 63 AND compute_region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$')
    );
CREATE INDEX apps_compute_region_in_use_idx ON apps (compute_region)
    WHERE compute_region <> '';

-- The region is customer intent. Scheduler hints, stale caches and direct
-- SQL cannot move an owner or instance outside that intent.
-- +goose StatementBegin
CREATE FUNCTION enforce_app_compute_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE node_region text;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.compute_region IS DISTINCT FROM OLD.compute_region THEN
        RAISE EXCEPTION 'compute region is immutable' USING ERRCODE = '23514', CONSTRAINT = 'apps_compute_region_immutable';
    END IF;
    IF NEW.compute_region <> '' AND NEW.node_id IS NOT NULL THEN
        SELECT region INTO node_region FROM compute_nodes WHERE id = NEW.node_id FOR SHARE;
        IF node_region IS DISTINCT FROM NEW.compute_region THEN
            RAISE EXCEPTION 'app owner is outside selected compute region' USING ERRCODE = '23514', CONSTRAINT = 'apps_compute_region_owner';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER apps_compute_region_guard BEFORE INSERT OR UPDATE OF compute_region, node_id
    ON apps FOR EACH ROW EXECUTE FUNCTION enforce_app_compute_region();

-- +goose StatementBegin
CREATE FUNCTION enforce_instance_compute_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE required_region text; node_region text;
BEGIN
    SELECT compute_region INTO required_region FROM apps WHERE id = NEW.app_id;
    IF coalesce(required_region, '') <> '' THEN
        SELECT region INTO node_region FROM compute_nodes WHERE id = NEW.node_id FOR SHARE;
        IF node_region IS DISTINCT FROM required_region THEN
            RAISE EXCEPTION 'instance is outside selected compute region' USING ERRCODE = '23514', CONSTRAINT = 'instances_compute_region';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER instances_compute_region_guard BEFORE INSERT OR UPDATE OF app_id, node_id
    ON instances FOR EACH ROW EXECUTE FUNCTION enforce_instance_compute_region();

-- A region label is an identity, not a scheduling hint. Keep every node in a
-- referenced region stable, including idle peers that an in-flight placement
-- or migration may already have selected. Retire and replace such nodes.
-- +goose StatementBegin
CREATE FUNCTION enforce_bound_compute_node_region() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.region IS DISTINCT FROM OLD.region AND (
        EXISTS (SELECT 1 FROM apps WHERE compute_region = OLD.region AND compute_region <> '') OR
        EXISTS (SELECT 1 FROM instances i JOIN apps a ON a.id = i.app_id
                WHERE i.node_id = OLD.id AND a.compute_region <> ''
                AND i.state IN ('waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm_paused'))
    ) THEN
        RAISE EXCEPTION 'node region is in use by region-bound apps' USING ERRCODE = '23514', CONSTRAINT = 'compute_nodes_region_in_use';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER compute_nodes_bound_region_guard BEFORE UPDATE OF region
    ON compute_nodes FOR EACH ROW EXECUTE FUNCTION enforce_bound_compute_node_region();

-- +goose Down
-- Do not silently remove a customer's placement constraint during rollback.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM apps WHERE compute_region <> '') THEN
        RAISE EXCEPTION 'cannot remove regional placement while region-bound apps exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER compute_nodes_bound_region_guard ON compute_nodes;
DROP FUNCTION enforce_bound_compute_node_region();
DROP TRIGGER instances_compute_region_guard ON instances;
DROP FUNCTION enforce_instance_compute_region();
DROP TRIGGER apps_compute_region_guard ON apps;
DROP FUNCTION enforce_app_compute_region();
ALTER TABLE apps DROP COLUMN compute_region;
