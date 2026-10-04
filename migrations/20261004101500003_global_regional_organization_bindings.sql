-- ADR-485: stable regional tenant anchors, separate from app creator metadata.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE global_owner_cell_bindings ALTER COLUMN regional_account_id DROP NOT NULL;
ALTER TABLE global_owner_cell_bindings ADD COLUMN regional_org_id uuid CHECK(regional_org_id <> '00000000-0000-0000-0000-000000000000');
ALTER TABLE global_owner_cell_bindings ADD CONSTRAINT global_regional_owner_exactly_one CHECK ((regional_account_id IS NOT NULL)::integer + (regional_org_id IS NOT NULL)::integer = 1);
ALTER TABLE global_owner_cell_bindings ADD CONSTRAINT global_regional_org_unique UNIQUE(cell_id,regional_org_id);
ALTER TABLE global_app_targets ADD COLUMN regional_org_id uuid CHECK(regional_org_id <> '00000000-0000-0000-0000-000000000000');
ALTER TABLE global_app_targets DROP CONSTRAINT global_app_targets_account_id_cell_id_regional_account_id_fkey;
ALTER TABLE global_app_targets ADD CONSTRAINT global_target_owner_cell_fkey FOREIGN KEY(owner_id,cell_id) REFERENCES global_owner_cell_bindings(owner_id,cell_id);
ALTER TABLE global_image_release_targets ADD COLUMN regional_org_id uuid CHECK(regional_org_id <> '00000000-0000-0000-0000-000000000000');
ALTER TABLE global_release_delegations ADD COLUMN regional_org_id uuid CHECK(regional_org_id <> '00000000-0000-0000-0000-000000000000');

CREATE FUNCTION lock_global_org_target_owner(wanted_owner uuid,actor uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE tenant uuid;
BEGIN
 PERFORM assert_global_directory_scope();
 SELECT org_id INTO tenant FROM global_directory_owners WHERE id=wanted_owner;
 IF tenant IS NULL THEN RAISE EXCEPTION 'shared owner required' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 PERFORM 1 FROM orgs WHERE id=tenant FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM orgs WHERE id=tenant AND NOT personal_org AND status='active' AND NOT deleted_pending) THEN
  RAISE EXCEPTION 'organization inactive' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_inactive';
 END IF;
 PERFORM 1 FROM accounts WHERE id=actor FOR SHARE;
 PERFORM 1 FROM org_memberships WHERE org_id=tenant AND account_id=actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM accounts a JOIN org_memberships m ON m.account_id=a.id AND m.org_id=tenant
   WHERE a.id=actor AND a.status='active' AND a.abuse_hold_at IS NULL AND a.email_verified_at IS NOT NULL
     AND m.removed_at IS NULL AND m.role IN ('owner','admin')) THEN
  RAISE EXCEPTION 'organization consent role required' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_forbidden';
 END IF;
 PERFORM 1 FROM global_directory_owners WHERE id=wanted_owner AND org_id=tenant FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'shared owner changed' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 RETURN wanted_owner;
END;
$$;

CREATE OR REPLACE FUNCTION reject_shared_global_target() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE tenant uuid; mapped_account uuid; mapped_org uuid;
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'regional tenant mappings retained' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM global_directory_owners WHERE id=OLD.owner_id) THEN
   RAISE EXCEPTION 'regional tenant mapping retained' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
  END IF;
  RETURN OLD;
 END IF;
 SELECT org_id INTO tenant FROM global_directory_owners WHERE id=NEW.owner_id FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'directory owner missing' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 IF tenant IS NOT NULL THEN
  IF NEW.regional_org_id IS NULL THEN RAISE EXCEPTION 'regional organization identity required' USING ERRCODE='23514',CONSTRAINT='global_directory_org_targets_unavailable'; END IF;
  PERFORM 1 FROM orgs WHERE id=tenant AND NOT personal_org AND status='active' AND NOT deleted_pending FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'organization inactive' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_inactive'; END IF;
 ELSIF NEW.regional_org_id IS NOT NULL THEN
  RAISE EXCEPTION 'personal owner requires personal proof' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
 END IF;
 IF TG_OP='UPDATE' AND NEW.regional_org_id IS DISTINCT FROM OLD.regional_org_id THEN
  RAISE EXCEPTION 'regional organization anchor immutable' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
 END IF;
 IF TG_TABLE_NAME='global_owner_cell_bindings' THEN
  IF (tenant IS NOT NULL AND NEW.regional_account_id IS NOT NULL) OR (tenant IS NULL AND NEW.regional_account_id IS NULL) THEN
   RAISE EXCEPTION 'regional tenant kind mismatch' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
  END IF;
 ELSE
  SELECT regional_account_id,regional_org_id INTO mapped_account,mapped_org FROM global_owner_cell_bindings WHERE owner_id=NEW.owner_id AND cell_id=NEW.cell_id FOR SHARE;
  IF NOT FOUND OR mapped_org IS DISTINCT FROM NEW.regional_org_id OR (tenant IS NULL AND mapped_account IS DISTINCT FROM NEW.regional_account_id) THEN
   RAISE EXCEPTION 'regional tenant does not match mapping' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_owner_cell_binding_delete BEFORE DELETE ON global_owner_cell_bindings FOR EACH ROW EXECUTE FUNCTION reject_shared_global_target();
CREATE TRIGGER global_owner_cell_binding_truncate BEFORE TRUNCATE ON global_owner_cell_bindings FOR EACH STATEMENT EXECUTE FUNCTION reject_shared_global_target();

-- Regional org identity is part of retained intent and encrypted authority.
CREATE FUNCTION protect_global_regional_org_anchor() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_org uuid; check_current boolean;
BEGIN
 IF TG_OP='UPDATE' AND NEW.regional_org_id IS DISTINCT FROM OLD.regional_org_id THEN
  RAISE EXCEPTION 'regional organization anchor immutable' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
 END IF;
 check_current := TG_OP='INSERT';
 IF TG_TABLE_NAME='global_release_delegations' THEN check_current := check_current OR NEW.status IN ('pending','active'); END IF;
 IF check_current THEN
  SELECT regional_org_id INTO current_org FROM global_app_targets WHERE global_app_id=NEW.global_app_id AND region=NEW.region;
  IF NOT FOUND OR current_org IS DISTINCT FROM NEW.regional_org_id THEN
   RAISE EXCEPTION 'regional organization anchor changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_release_target_org_anchor BEFORE INSERT OR UPDATE ON global_image_release_targets FOR EACH ROW EXECUTE FUNCTION protect_global_regional_org_anchor();
CREATE TRIGGER global_delegation_org_anchor BEFORE INSERT OR UPDATE ON global_release_delegations FOR EACH ROW EXECUTE FUNCTION protect_global_regional_org_anchor();

-- Identity attachment is available; runtime admission remains gated in SQL too.
CREATE FUNCTION reject_shared_global_runtime() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM global_apps a JOIN global_directory_owners o ON o.id=a.owner_id WHERE a.id=NEW.global_app_id AND o.org_id IS NOT NULL) THEN
  RAISE EXCEPTION 'shared runtime admission pending' USING ERRCODE='23514',CONSTRAINT='global_directory_org_targets_unavailable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_release_shared_runtime BEFORE INSERT ON global_image_releases FOR EACH ROW EXECUTE FUNCTION reject_shared_global_runtime();
CREATE TRIGGER global_delegation_shared_runtime BEFORE INSERT ON global_release_delegations FOR EACH ROW EXECUTE FUNCTION reject_shared_global_runtime();

CREATE OR REPLACE FUNCTION plan_global_image_release(owner_id uuid, wanted_slug text, wanted_release uuid, wanted_image text) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE app global_apps%ROWTYPE; old global_image_releases%ROWTYPE; target_count bigint;
BEGIN
    PERFORM assert_global_directory_scope();
    SELECT * INTO app FROM global_apps WHERE global_apps.owner_id=plan_global_image_release.owner_id AND slug=wanted_slug FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    IF EXISTS(SELECT 1 FROM global_directory_owners WHERE id=app.owner_id AND org_id IS NOT NULL) THEN
        RAISE EXCEPTION 'shared runtime admission pending' USING ERRCODE='23514',CONSTRAINT='global_directory_org_targets_unavailable';
    END IF;
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
    INSERT INTO global_image_release_targets(global_app_id,release_id,region,cell_id,cell_generation,api_base,regional_account_id,regional_org_id,regional_app_id,regional_app_slug,verified_at)
        SELECT t.global_app_id,wanted_release,t.region,t.cell_id,t.cell_generation,c.api_base,t.regional_account_id,t.regional_org_id,t.regional_app_id,t.regional_app_slug,t.verified_at
        FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id WHERE t.global_app_id=app.id;
    UPDATE global_apps SET desired_release_generation=app.desired_release_generation+1 WHERE id=app.id;
    RETURN app.id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM global_owner_cell_bindings WHERE regional_org_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM global_app_targets WHERE regional_org_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM global_image_release_targets WHERE regional_org_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM global_release_delegations WHERE regional_org_id IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot downgrade regional organization bindings';
 END IF;
END $$;
DROP TRIGGER global_release_shared_runtime ON global_image_releases;
DROP TRIGGER global_delegation_shared_runtime ON global_release_delegations;
DROP FUNCTION reject_shared_global_runtime();
DROP TRIGGER global_release_target_org_anchor ON global_image_release_targets;
DROP TRIGGER global_delegation_org_anchor ON global_release_delegations;
DROP FUNCTION protect_global_regional_org_anchor();
DROP TRIGGER global_owner_cell_binding_delete ON global_owner_cell_bindings;
DROP TRIGGER global_owner_cell_binding_truncate ON global_owner_cell_bindings;
DROP FUNCTION lock_global_org_target_owner(uuid,uuid);
ALTER TABLE global_app_targets DROP CONSTRAINT global_target_owner_cell_fkey;
ALTER TABLE global_app_targets ADD CONSTRAINT global_app_targets_account_id_cell_id_regional_account_id_fkey FOREIGN KEY(owner_id,cell_id,regional_account_id) REFERENCES global_owner_cell_bindings(owner_id,cell_id,regional_account_id);
ALTER TABLE global_owner_cell_bindings DROP CONSTRAINT global_regional_owner_exactly_one;
ALTER TABLE global_owner_cell_bindings DROP CONSTRAINT global_regional_org_unique;
ALTER TABLE global_owner_cell_bindings ALTER COLUMN regional_account_id SET NOT NULL;
ALTER TABLE global_owner_cell_bindings DROP COLUMN regional_org_id;
ALTER TABLE global_app_targets DROP COLUMN regional_org_id;
ALTER TABLE global_image_release_targets DROP COLUMN regional_org_id;
ALTER TABLE global_release_delegations DROP COLUMN regional_org_id;
CREATE OR REPLACE FUNCTION reject_shared_global_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM global_directory_owners WHERE id=NEW.owner_id AND account_id IS NOT NULL FOR SHARE;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'shared regional authorization not available' USING ERRCODE='23514', CONSTRAINT='global_directory_org_targets_unavailable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION plan_global_image_release(owner_id uuid, wanted_slug text, wanted_release uuid, wanted_image text) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE app global_apps%ROWTYPE; old global_image_releases%ROWTYPE; target_count bigint;
BEGIN
    PERFORM assert_global_directory_scope();
    SELECT * INTO app FROM global_apps WHERE global_apps.owner_id=plan_global_image_release.owner_id AND slug=wanted_slug FOR UPDATE;
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
