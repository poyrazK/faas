-- filename: 20261003143601470_regional_control_plane_cell.sql
-- ADR-475: fresh independent regional control-plane databases.
-- +goose Up
CREATE TABLE IF NOT EXISTS control_plane_cells (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    id uuid NOT NULL UNIQUE CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    compute_region text NOT NULL REFERENCES compute_regions(id)
        CHECK (length(compute_region) BETWEEN 1 AND 63 AND compute_region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_control_plane_cell() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'control plane cell identity is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'control_plane_cell_immutable';
    END IF;
    -- Freeze adoption and customer creation while checking the fresh database.
    LOCK TABLE accounts, apps, projects, jobs, instances, compute_nodes IN SHARE ROW EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM accounts) OR EXISTS (SELECT 1 FROM apps)
       OR EXISTS (SELECT 1 FROM projects) OR EXISTS (SELECT 1 FROM jobs)
       OR EXISTS (SELECT 1 FROM instances)
       OR EXISTS (SELECT 1 FROM compute_nodes WHERE NOT coalesce((
           name = 'default-local' AND target_url = 'unix:///run/faas/vmmd.sock'
           AND region = 'local' AND zone = 'local'
           AND release_id IS NULL AND manifest_hash IS NULL AND role IS NULL
           AND host_certificate IS NULL AND cert_fingerprint IS NULL
           AND last_heartbeat_at = created_at), false)) THEN
        RAISE EXCEPTION 'cell initialization requires a fresh database'
            USING ERRCODE = '23514', CONSTRAINT = 'control_plane_cell_fresh';
    END IF;
    DELETE FROM compute_nodes WHERE name = 'default-local';
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION initialize_control_plane_cell(wanted_id uuid, wanted_region text) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE existing control_plane_cells%ROWTYPE;
BEGIN
    LOCK TABLE control_plane_cells IN EXCLUSIVE MODE;
    SELECT * INTO existing FROM control_plane_cells WHERE singleton;
    IF FOUND THEN
        IF existing.id IS DISTINCT FROM wanted_id OR existing.compute_region IS DISTINCT FROM wanted_region THEN
            RAISE EXCEPTION 'database belongs to a different control plane cell'
                USING ERRCODE = '23514', CONSTRAINT = 'control_plane_cell_identity';
        END IF;
        RETURN existing.id;
    END IF;
    INSERT INTO compute_regions(id, display_name, accepting_new_apps)
        VALUES (wanted_region, wanted_region, true) ON CONFLICT (id) DO NOTHING;
    INSERT INTO control_plane_cells(id, compute_region) VALUES (wanted_id, wanted_region);
    RETURN wanted_id;
END;
$$;
CREATE OR REPLACE FUNCTION enforce_control_plane_cell_node() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE required_region text;
BEGIN
    SELECT compute_region INTO required_region FROM control_plane_cells WHERE singleton;
    IF FOUND AND NEW.region IS DISTINCT FROM required_region THEN
        RAISE EXCEPTION 'compute node is outside control plane cell region'
            USING ERRCODE = '23514', CONSTRAINT = 'control_plane_cell_region', DETAIL = coalesce(NEW.region, '');
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION enforce_control_plane_cell_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE required_region text;
BEGIN
    SELECT compute_region INTO required_region FROM control_plane_cells WHERE singleton;
    IF FOUND AND NEW.compute_region <> '' AND NEW.compute_region IS DISTINCT FROM required_region THEN
        RAISE EXCEPTION 'workload is outside control plane cell region'
            USING ERRCODE = '23514', CONSTRAINT = 'control_plane_cell_region', DETAIL = NEW.compute_region;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS control_plane_cell_identity_guard ON control_plane_cells;
CREATE TRIGGER control_plane_cell_identity_guard BEFORE INSERT OR UPDATE OR DELETE ON control_plane_cells
    FOR EACH ROW EXECUTE FUNCTION protect_control_plane_cell();
DROP TRIGGER IF EXISTS control_plane_cell_truncate_guard ON control_plane_cells;
CREATE TRIGGER control_plane_cell_truncate_guard BEFORE TRUNCATE ON control_plane_cells
    FOR EACH STATEMENT EXECUTE FUNCTION protect_control_plane_cell();
DROP TRIGGER IF EXISTS compute_nodes_cell_region_guard ON compute_nodes;
CREATE TRIGGER compute_nodes_cell_region_guard BEFORE INSERT OR UPDATE OF region ON compute_nodes
    FOR EACH ROW EXECUTE FUNCTION enforce_control_plane_cell_node();
DROP TRIGGER IF EXISTS apps_cell_region_guard ON apps;
CREATE TRIGGER apps_cell_region_guard BEFORE INSERT OR UPDATE OF compute_region ON apps
    FOR EACH ROW EXECUTE FUNCTION enforce_control_plane_cell_intent();
DROP TRIGGER IF EXISTS projects_cell_region_guard ON projects;
CREATE TRIGGER projects_cell_region_guard BEFORE INSERT OR UPDATE OF compute_region ON projects
    FOR EACH ROW EXECUTE FUNCTION enforce_control_plane_cell_intent();
DROP TRIGGER IF EXISTS jobs_cell_region_guard ON jobs;
CREATE TRIGGER jobs_cell_region_guard BEFORE INSERT OR UPDATE OF compute_region ON jobs
    FOR EACH ROW EXECUTE FUNCTION enforce_control_plane_cell_intent();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM control_plane_cells) THEN
        RAISE EXCEPTION 'cannot downgrade a bound control plane cell';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS jobs_cell_region_guard ON jobs;
DROP TRIGGER IF EXISTS projects_cell_region_guard ON projects;
DROP TRIGGER IF EXISTS apps_cell_region_guard ON apps;
DROP TRIGGER IF EXISTS compute_nodes_cell_region_guard ON compute_nodes;
DROP FUNCTION IF EXISTS enforce_control_plane_cell_intent();
DROP FUNCTION IF EXISTS enforce_control_plane_cell_node();
DROP FUNCTION IF EXISTS initialize_control_plane_cell(uuid, text);
DROP TABLE IF EXISTS control_plane_cells;
DROP FUNCTION IF EXISTS protect_control_plane_cell();
