-- filename: 20261004014000527_global_image_release_journal.sql
-- ADR-480. Bounds mirror pkg/api/limits.go.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE global_apps ADD COLUMN desired_release_generation bigint NOT NULL DEFAULT 0
    CHECK (desired_release_generation BETWEEN 0 AND 9007199254740991);
CREATE TABLE global_image_releases (
    global_app_id uuid NOT NULL REFERENCES global_apps(id) ON DELETE CASCADE,
    release_id uuid NOT NULL CHECK (release_id <> '00000000-0000-0000-0000-000000000000'),
    generation bigint NOT NULL CHECK (generation BETWEEN 1 AND 9007199254740991),
    image_ref text NOT NULL CHECK (length(image_ref) BETWEEN 1 AND 2048 AND image_ref ~ '^.+@sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (global_app_id, release_id),
    UNIQUE (global_app_id, generation)
);
CREATE TABLE global_image_release_targets (
    global_app_id uuid NOT NULL,
    release_id uuid NOT NULL,
    region text NOT NULL CHECK (length(region) BETWEEN 1 AND 63 AND region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    cell_id uuid NOT NULL CHECK (cell_id <> '00000000-0000-0000-0000-000000000000'),
    cell_generation bigint NOT NULL CHECK (cell_generation > 0),
    api_base text NOT NULL CHECK (length(api_base) <= 2048 AND api_base ~ '^https://([a-z0-9.-]+|\[[0-9a-f:]+\])(:[0-9]{1,5})?$'),
    regional_account_id uuid NOT NULL CHECK (regional_account_id <> '00000000-0000-0000-0000-000000000000'),
    regional_app_id uuid NOT NULL CHECK (regional_app_id <> '00000000-0000-0000-0000-000000000000'),
    regional_app_slug text NOT NULL CHECK (length(regional_app_slug) BETWEEN 3 AND 40 AND regional_app_slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
    verified_at timestamptz NOT NULL,
    outcome text NOT NULL DEFAULT 'pending' CHECK (outcome IN ('pending','accepted','expired','blocked')),
    diagnostic text NOT NULL DEFAULT '' CHECK (diagnostic IN ('','regional_unavailable','regional_rejected','identity_changed','receipt_invalid')),
    deployment_id uuid CHECK (deployment_id <> '00000000-0000-0000-0000-000000000000'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (global_app_id, release_id, region),
    FOREIGN KEY (global_app_id, release_id) REFERENCES global_image_releases(global_app_id, release_id) ON DELETE CASCADE,
    CHECK ((outcome = 'accepted' AND deployment_id IS NOT NULL AND diagnostic = '') OR
           (outcome = 'expired' AND deployment_id IS NULL AND diagnostic = '') OR
           (outcome = 'blocked' AND deployment_id IS NULL AND diagnostic = 'identity_changed') OR
           (outcome = 'pending' AND deployment_id IS NULL AND diagnostic IN ('','regional_unavailable','regional_rejected','receipt_invalid')))
);

CREATE FUNCTION lock_global_release_target_app() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM 1 FROM global_apps WHERE id = OLD.global_app_id FOR UPDATE;
        RETURN OLD;
    END IF;
    PERFORM 1 FROM global_apps WHERE id = NEW.global_app_id FOR UPDATE;
    RETURN NEW;
END;
$$;
CREATE TRIGGER global_app_targets_allocation_lock BEFORE INSERT OR UPDATE OR DELETE ON global_app_targets
    FOR EACH ROW EXECUTE FUNCTION lock_global_release_target_app();

CREATE FUNCTION protect_global_image_release() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE head bigint; snapshot_count bigint; target_count bigint;
BEGIN
    PERFORM assert_global_directory_scope();
    IF TG_OP = 'TRUNCATE' THEN
        RAISE EXCEPTION 'global release history cannot be truncated' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
    END IF;
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM global_apps WHERE id = OLD.global_app_id) THEN
            RAISE EXCEPTION 'global release history is retained' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
        END IF;
        RETURN OLD;
    END IF;
    IF TG_TABLE_NAME = 'global_apps' THEN
        IF TG_OP = 'INSERT' THEN
            IF NEW.desired_release_generation <> 0 THEN
                RAISE EXCEPTION 'new global app must have no release head' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
            END IF;
            RETURN NEW;
        END IF;
        IF NEW.desired_release_generation = OLD.desired_release_generation THEN RETURN NEW; END IF;
        IF NEW.desired_release_generation <> OLD.desired_release_generation + 1 THEN
            RAISE EXCEPTION 'global release generation must increase by one' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
        END IF;
        PERFORM 1 FROM global_image_releases WHERE global_app_id = NEW.id AND generation = NEW.desired_release_generation;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'global release head requires durable intent' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
        END IF;
        SELECT count(*) INTO target_count FROM global_app_targets WHERE global_app_id = NEW.id;
        SELECT count(*) INTO snapshot_count FROM global_image_release_targets rt
            JOIN global_image_releases r USING(global_app_id,release_id)
            JOIN global_app_targets t ON t.global_app_id = rt.global_app_id AND t.region = rt.region
            JOIN global_directory_cells c ON c.id = t.cell_id
            WHERE r.global_app_id = NEW.id AND r.generation = NEW.desired_release_generation
                AND rt.cell_id = t.cell_id AND rt.cell_generation = t.cell_generation
                AND rt.regional_account_id = t.regional_account_id AND rt.regional_app_id = t.regional_app_id
                AND rt.regional_app_slug = t.regional_app_slug AND rt.verified_at = t.verified_at
                AND c.enabled AND c.generation = rt.cell_generation AND c.api_base = rt.api_base;
        IF target_count NOT BETWEEN 1 AND 32 OR snapshot_count <> target_count THEN
            RAISE EXCEPTION 'release requires complete current target snapshot' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
        END IF;
        RETURN NEW;
    END IF;
    SELECT desired_release_generation INTO head FROM global_apps WHERE id = NEW.global_app_id FOR UPDATE;
    IF TG_TABLE_NAME = 'global_image_releases' THEN
        IF TG_OP = 'UPDATE' OR NEW.generation <> head + 1 THEN
            RAISE EXCEPTION 'global release intent is immutable and ordered' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
        END IF;
    ELSIF TG_TABLE_NAME = 'global_image_release_targets' THEN
        IF TG_OP = 'UPDATE' THEN
            IF (NEW.global_app_id,NEW.release_id,NEW.region,NEW.cell_id,NEW.cell_generation,NEW.api_base,NEW.regional_account_id,NEW.regional_app_id,NEW.regional_app_slug,NEW.verified_at)
                IS DISTINCT FROM (OLD.global_app_id,OLD.release_id,OLD.region,OLD.cell_id,OLD.cell_generation,OLD.api_base,OLD.regional_account_id,OLD.regional_app_id,OLD.regional_app_slug,OLD.verified_at)
                OR OLD.outcome <> 'pending' THEN
                RAISE EXCEPTION 'release snapshot and terminal result are immutable' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
            END IF;
            PERFORM 1 FROM global_image_releases WHERE global_app_id = NEW.global_app_id AND release_id = NEW.release_id AND generation = head;
        ELSE
            PERFORM 1 FROM global_image_releases WHERE global_app_id = NEW.global_app_id AND release_id = NEW.release_id AND generation = head + 1;
            IF NEW.outcome <> 'pending' OR NEW.diagnostic <> '' THEN
                RAISE EXCEPTION 'new target must be pending' USING ERRCODE = '23514', CONSTRAINT = 'global_release_identity';
            END IF;
        END IF;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'superseded release cannot publish results' USING ERRCODE = '23514', CONSTRAINT = 'global_release_superseded';
        END IF;
        IF NEW.outcome <> 'blocked' THEN
            PERFORM 1 FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id
                WHERE t.global_app_id=NEW.global_app_id AND t.region=NEW.region AND t.cell_id=NEW.cell_id
                    AND t.cell_generation=NEW.cell_generation AND t.regional_account_id=NEW.regional_account_id
                    AND t.regional_app_id=NEW.regional_app_id AND t.regional_app_slug=NEW.regional_app_slug
                    AND c.enabled AND c.generation=NEW.cell_generation AND c.api_base=NEW.api_base
                FOR SHARE OF c;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'release target identity changed' USING ERRCODE = '23514', CONSTRAINT = 'global_directory_stale_cell';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER global_apps_release_head_guard BEFORE INSERT OR UPDATE OF desired_release_generation ON global_apps FOR EACH ROW EXECUTE FUNCTION protect_global_image_release();
CREATE TRIGGER global_image_releases_guard BEFORE INSERT OR UPDATE OR DELETE ON global_image_releases FOR EACH ROW EXECUTE FUNCTION protect_global_image_release();
CREATE TRIGGER global_image_release_targets_guard BEFORE INSERT OR UPDATE OR DELETE ON global_image_release_targets FOR EACH ROW EXECUTE FUNCTION protect_global_image_release();
CREATE TRIGGER global_image_releases_truncate_guard BEFORE TRUNCATE ON global_image_releases FOR EACH STATEMENT EXECUTE FUNCTION protect_global_image_release();
CREATE TRIGGER global_image_release_targets_truncate_guard BEFORE TRUNCATE ON global_image_release_targets FOR EACH STATEMENT EXECUTE FUNCTION protect_global_image_release();

CREATE FUNCTION plan_global_image_release(owner_id uuid, wanted_slug text, wanted_release uuid, wanted_image text) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE app global_apps%ROWTYPE; old global_image_releases%ROWTYPE; target_count bigint;
BEGIN
    PERFORM assert_global_directory_scope();
    SELECT * INTO app FROM global_apps WHERE account_id=owner_id AND slug=wanted_slug FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    SELECT * INTO old FROM global_image_releases WHERE global_app_id=app.id AND release_id=wanted_release;
    IF FOUND THEN
        IF old.image_ref<>wanted_image THEN
            RAISE EXCEPTION 'release image changed' USING ERRCODE='23514',CONSTRAINT='global_release_identity';
        END IF;
        RETURN app.id;
    END IF;
    SELECT count(*) INTO target_count FROM global_app_targets WHERE global_app_id=app.id;
    IF target_count=0 THEN
        RAISE EXCEPTION 'release has no targets' USING ERRCODE='23514',CONSTRAINT='global_release_no_targets';
    END IF;
    IF target_count>32 THEN
        RAISE EXCEPTION 'global release bound reached' USING ERRCODE='23514',CONSTRAINT='global_release_limit',DETAIL='targets:32:'||target_count::text;
    END IF;
    IF app.desired_release_generation>=9007199254740991 THEN
        RAISE EXCEPTION 'global release bound reached' USING ERRCODE='23514',CONSTRAINT='global_release_limit',DETAIL='generation:9007199254740991:'||(app.desired_release_generation+1)::text;
    END IF;
    PERFORM 1 FROM global_directory_cells c JOIN global_app_targets t ON t.cell_id=c.id
        WHERE t.global_app_id=app.id ORDER BY c.id FOR SHARE OF c;
    IF EXISTS (SELECT 1 FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id
        WHERE t.global_app_id=app.id AND (NOT c.enabled OR c.generation<>t.cell_generation)) THEN
        RAISE EXCEPTION 'release target requires verification' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell';
    END IF;
    INSERT INTO global_image_releases(global_app_id,release_id,generation,image_ref)
        VALUES(app.id,wanted_release,app.desired_release_generation+1,wanted_image);
    INSERT INTO global_image_release_targets(global_app_id,release_id,region,cell_id,cell_generation,api_base,regional_account_id,regional_app_id,regional_app_slug,verified_at)
        SELECT t.global_app_id,wanted_release,t.region,t.cell_id,t.cell_generation,c.api_base,t.regional_account_id,t.regional_app_id,t.regional_app_slug,t.verified_at
        FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id WHERE t.global_app_id=app.id;
    UPDATE global_apps SET desired_release_generation=app.desired_release_generation+1 WHERE id=app.id;
    RETURN app.id;
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM global_image_releases) THEN RAISE EXCEPTION 'cannot downgrade populated global image releases'; END IF;
END $$;
DROP FUNCTION plan_global_image_release(uuid,text,uuid,text);
DROP TRIGGER global_apps_release_head_guard ON global_apps;
DROP TABLE global_image_release_targets,global_image_releases;
DROP FUNCTION protect_global_image_release();
DROP TRIGGER global_app_targets_allocation_lock ON global_app_targets;
DROP FUNCTION lock_global_release_target_app();
ALTER TABLE global_apps DROP COLUMN desired_release_generation;
-- +goose StatementEnd
