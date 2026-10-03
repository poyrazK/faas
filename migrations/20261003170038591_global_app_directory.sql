-- filename: 20261003170038591_global_app_directory.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE global_directory_cells (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    region text NOT NULL UNIQUE CHECK (length(region) BETWEEN 1 AND 63 AND region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    api_base text NOT NULL CHECK (length(api_base) <= 2048 AND api_base ~ '^https://([a-z0-9.-]+|\[[0-9a-f:]+\])(:[0-9]{1,5})?$'),
    enabled boolean NOT NULL,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    UNIQUE (id, region)
);
CREATE TABLE global_apps (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid() CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    slug text NOT NULL UNIQUE CHECK (length(slug) BETWEEN 3 AND 40 AND slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    routing_policy text NOT NULL DEFAULT 'all_selected_regions' CHECK (routing_policy = 'all_selected_regions'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, account_id)
);
CREATE TABLE global_account_cell_bindings (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    cell_id uuid NOT NULL REFERENCES global_directory_cells(id),
    regional_account_id uuid NOT NULL CHECK (regional_account_id <> '00000000-0000-0000-0000-000000000000'),
    PRIMARY KEY (account_id, cell_id),
    UNIQUE (cell_id, regional_account_id),
    UNIQUE (account_id, cell_id, regional_account_id)
);
CREATE TABLE global_app_targets (
    global_app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    cell_id uuid NOT NULL,
    region text NOT NULL,
    cell_generation bigint NOT NULL CHECK (cell_generation > 0),
    regional_account_id uuid NOT NULL,
    regional_app_id uuid NOT NULL CHECK (regional_app_id <> '00000000-0000-0000-0000-000000000000'),
    regional_app_slug text NOT NULL CHECK (length(regional_app_slug) BETWEEN 3 AND 40 AND regional_app_slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    verified_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (global_app_id, region),
    UNIQUE (cell_id, regional_app_id),
    FOREIGN KEY (global_app_id, account_id) REFERENCES global_apps(id, account_id) ON DELETE CASCADE,
    FOREIGN KEY (cell_id, region) REFERENCES global_directory_cells(id, region),
    FOREIGN KEY (account_id, cell_id, regional_account_id) REFERENCES global_account_cell_bindings(account_id, cell_id, regional_account_id)
);

CREATE FUNCTION assert_global_directory_scope() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize directory writes with cell initialization. This also works
    -- for an INSERT bypassing initialize_control_plane_cell.
    LOCK TABLE control_plane_cells IN SHARE MODE;
    IF EXISTS (SELECT 1 FROM control_plane_cells) THEN
        RAISE EXCEPTION 'regional databases cannot own the global directory'
            USING ERRCODE = '23514', CONSTRAINT = 'global_directory_scope';
    END IF;
END;
$$;
CREATE FUNCTION protect_global_directory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM assert_global_directory_scope();
    IF TG_TABLE_NAME = 'global_directory_cells' AND TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id OR NEW.region IS DISTINCT FROM OLD.region THEN
            RAISE EXCEPTION 'directory cell identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
        NEW.generation := OLD.generation + CASE WHEN NEW.api_base IS DISTINCT FROM OLD.api_base OR NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END;
    ELSIF TG_TABLE_NAME = 'global_apps' AND TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.slug IS DISTINCT FROM OLD.slug OR NEW.routing_policy IS DISTINCT FROM OLD.routing_policy THEN
            RAISE EXCEPTION 'global app identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    ELSIF TG_TABLE_NAME = 'global_account_cell_bindings' AND TG_OP = 'UPDATE' THEN
        IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.cell_id IS DISTINCT FROM OLD.cell_id OR NEW.regional_account_id IS DISTINCT FROM OLD.regional_account_id THEN
            RAISE EXCEPTION 'regional account binding is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    ELSIF TG_TABLE_NAME = 'global_app_targets' AND TG_OP <> 'DELETE' THEN
        PERFORM 1 FROM global_directory_cells
            WHERE id = NEW.cell_id AND region = NEW.region AND enabled AND generation = NEW.cell_generation FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'cell changed during target verification'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_stale_cell';
        END IF;
        IF TG_OP = 'UPDATE' AND (NEW.global_app_id IS DISTINCT FROM OLD.global_app_id OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.region IS DISTINCT FROM OLD.region OR NEW.cell_id IS DISTINCT FROM OLD.cell_id OR NEW.regional_account_id IS DISTINCT FROM OLD.regional_account_id OR NEW.regional_app_id IS DISTINCT FROM OLD.regional_app_id OR NEW.regional_app_slug IS DISTINCT FROM OLD.regional_app_slug) THEN
            RAISE EXCEPTION 'regional app binding is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER global_directory_cells_guard BEFORE INSERT OR UPDATE OR DELETE ON global_directory_cells FOR EACH ROW EXECUTE FUNCTION protect_global_directory();
CREATE TRIGGER global_apps_guard BEFORE INSERT OR UPDATE OR DELETE ON global_apps FOR EACH ROW EXECUTE FUNCTION protect_global_directory();
CREATE TRIGGER global_account_cell_bindings_guard BEFORE INSERT OR UPDATE OR DELETE ON global_account_cell_bindings FOR EACH ROW EXECUTE FUNCTION protect_global_directory();
CREATE TRIGGER global_app_targets_guard BEFORE INSERT OR UPDATE OR DELETE ON global_app_targets FOR EACH ROW EXECUTE FUNCTION protect_global_directory();

CREATE FUNCTION guard_cell_from_global_directory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM global_directory_cells) OR EXISTS (SELECT 1 FROM global_apps) THEN
        RAISE EXCEPTION 'a populated global directory cannot become a regional cell'
            USING ERRCODE = '23514', CONSTRAINT = 'global_directory_scope';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER control_plane_cell_directory_guard BEFORE INSERT ON control_plane_cells FOR EACH ROW EXECUTE FUNCTION guard_cell_from_global_directory();

CREATE FUNCTION create_global_app(owner_id uuid, wanted_slug text, app_limit integer) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE existing global_apps%ROWTYPE; wanted_id uuid;
BEGIN
    PERFORM assert_global_directory_scope();
    PERFORM 1 FROM accounts WHERE id = owner_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'directory account missing' USING ERRCODE = '23503';
    END IF;
    SELECT * INTO existing FROM global_apps WHERE slug = wanted_slug;
    IF FOUND THEN
        IF existing.account_id <> owner_id THEN
            RAISE EXCEPTION 'global slug taken' USING ERRCODE = '23505';
        END IF;
        RETURN existing.id;
    END IF;
    IF app_limit < 1 OR (SELECT count(*) FROM global_apps WHERE account_id = owner_id) >= app_limit THEN
        RAISE EXCEPTION 'global app identity limit reached'
            USING ERRCODE = '23514', CONSTRAINT = 'global_directory_limit';
    END IF;
    INSERT INTO global_apps(account_id, slug) VALUES (owner_id, wanted_slug) RETURNING id INTO wanted_id;
    RETURN wanted_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM global_apps) OR EXISTS (SELECT 1 FROM global_directory_cells) THEN
        RAISE EXCEPTION 'cannot downgrade a populated global app directory';
    END IF;
END $$;
DROP TRIGGER control_plane_cell_directory_guard ON control_plane_cells;
DROP FUNCTION guard_cell_from_global_directory();
DROP FUNCTION create_global_app(uuid, text, integer);
DROP TABLE global_app_targets, global_account_cell_bindings, global_apps, global_directory_cells;
DROP FUNCTION protect_global_directory();
DROP FUNCTION assert_global_directory_scope();
-- +goose StatementEnd
