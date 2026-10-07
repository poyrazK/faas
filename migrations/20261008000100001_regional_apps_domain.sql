-- ADR-657: independent, explicitly registered regional traffic suffix.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION valid_regional_apps_domain(domain text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE label text;
BEGIN
    -- ADR-657, pkg/api/limits.go: 253 - 40 - 1 bytes for the DNS suffix.
    IF octet_length(domain) > 212 OR position('.' in domain) = 0 OR
       domain !~ '^[a-z0-9.-]+$' COLLATE "C" OR domain ~ '^[0-9.]+$' COLLATE "C" THEN
        RETURN false;
    END IF;
    FOREACH label IN ARRAY string_to_array(domain, '.') LOOP
        IF octet_length(label) < 1 OR octet_length(label) > 63 OR
           left(label,1) = '-' OR right(label,1) = '-' THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
END;
$$;
-- +goose StatementEnd

ALTER TABLE global_directory_cells ADD COLUMN apps_domain text NOT NULL DEFAULT '';
ALTER TABLE global_directory_cells ADD CONSTRAINT global_directory_cells_apps_domain_check
 CHECK (apps_domain = '' OR valid_regional_apps_domain(apps_domain));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_global_directory() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM assert_global_directory_scope();
    IF TG_TABLE_NAME = 'global_directory_cells' AND TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id OR NEW.region IS DISTINCT FROM OLD.region THEN
            RAISE EXCEPTION 'directory cell identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
        NEW.generation := OLD.generation + CASE WHEN NEW.api_base IS DISTINCT FROM OLD.api_base OR NEW.enabled IS DISTINCT FROM OLD.enabled OR NEW.apps_domain IS DISTINCT FROM OLD.apps_domain THEN 1 ELSE 0 END;
    ELSIF TG_TABLE_NAME = 'global_apps' AND TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id OR NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.slug IS DISTINCT FROM OLD.slug OR NEW.routing_policy IS DISTINCT FROM OLD.routing_policy THEN
            RAISE EXCEPTION 'global app identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    ELSIF TG_TABLE_NAME = 'global_owner_cell_bindings' AND TG_OP = 'UPDATE' THEN
        IF NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.cell_id IS DISTINCT FROM OLD.cell_id OR NEW.regional_account_id IS DISTINCT FROM OLD.regional_account_id THEN
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
        IF TG_OP = 'UPDATE' AND (NEW.global_app_id IS DISTINCT FROM OLD.global_app_id OR NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.region IS DISTINCT FROM OLD.region OR NEW.cell_id IS DISTINCT FROM OLD.cell_id OR NEW.regional_account_id IS DISTINCT FROM OLD.regional_account_id OR NEW.regional_app_id IS DISTINCT FROM OLD.regional_app_id OR NEW.regional_app_slug IS DISTINCT FROM OLD.regional_app_slug) THEN
            RAISE EXCEPTION 'regional app binding is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM global_directory_cells WHERE apps_domain <> '') THEN
  RAISE EXCEPTION 'clear registered traffic suffixes explicitly before rollback' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_global_directory() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM assert_global_directory_scope();
    IF TG_TABLE_NAME = 'global_directory_cells' AND TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id OR NEW.region IS DISTINCT FROM OLD.region THEN
            RAISE EXCEPTION 'directory cell identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
        NEW.generation := OLD.generation + CASE WHEN NEW.api_base IS DISTINCT FROM OLD.api_base OR NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END;
    ELSIF TG_TABLE_NAME = 'global_apps' AND TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id OR NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.slug IS DISTINCT FROM OLD.slug OR NEW.routing_policy IS DISTINCT FROM OLD.routing_policy THEN
            RAISE EXCEPTION 'global app identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    ELSIF TG_TABLE_NAME = 'global_owner_cell_bindings' AND TG_OP = 'UPDATE' THEN
        IF NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.cell_id IS DISTINCT FROM OLD.cell_id OR NEW.regional_account_id IS DISTINCT FROM OLD.regional_account_id THEN
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
        IF TG_OP = 'UPDATE' AND (NEW.global_app_id IS DISTINCT FROM OLD.global_app_id OR NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.region IS DISTINCT FROM OLD.region OR NEW.cell_id IS DISTINCT FROM OLD.cell_id OR NEW.regional_account_id IS DISTINCT FROM OLD.regional_account_id OR NEW.regional_app_id IS DISTINCT FROM OLD.regional_app_id OR NEW.regional_app_slug IS DISTINCT FROM OLD.regional_app_slug) THEN
            RAISE EXCEPTION 'regional app binding is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'global_directory_identity';
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

ALTER TABLE global_directory_cells DROP COLUMN apps_domain;
DROP FUNCTION valid_regional_apps_domain(text);
