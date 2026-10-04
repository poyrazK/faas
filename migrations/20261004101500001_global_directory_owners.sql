-- filename: 20261004101500001_global_directory_owners.sql
-- ADR-483: tenant roots are independent of the actor who registers an app.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE global_directory_owners (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id <> '00000000-0000-0000-0000-000000000000'),
 account_id uuid UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
 org_id uuid UNIQUE REFERENCES orgs(id) ON DELETE CASCADE,
 CHECK ((account_id IS NOT NULL)::integer + (org_id IS NOT NULL)::integer = 1),
 CHECK (account_id IS NULL OR id = account_id)
);
INSERT INTO global_directory_owners(id,account_id)
 SELECT account_id,account_id FROM global_apps UNION SELECT account_id,account_id FROM global_account_cell_bindings;
ALTER TABLE global_apps DROP CONSTRAINT global_apps_account_id_fkey;
ALTER TABLE global_account_cell_bindings DROP CONSTRAINT global_account_cell_bindings_account_id_fkey;
ALTER TABLE global_apps RENAME COLUMN account_id TO owner_id;
ALTER TABLE global_account_cell_bindings RENAME TO global_owner_cell_bindings;
ALTER TABLE global_owner_cell_bindings RENAME COLUMN account_id TO owner_id;
ALTER TABLE global_app_targets RENAME COLUMN account_id TO owner_id;
ALTER TABLE global_release_delegations RENAME COLUMN account_id TO owner_id;
ALTER TABLE global_release_recovery_work RENAME COLUMN account_id TO owner_id;
ALTER TABLE global_apps ADD CONSTRAINT global_apps_owner_id_fkey FOREIGN KEY(owner_id) REFERENCES global_directory_owners(id) ON DELETE CASCADE;
ALTER TABLE global_owner_cell_bindings ADD CONSTRAINT global_owner_cell_bindings_owner_id_fkey FOREIGN KEY(owner_id) REFERENCES global_directory_owners(id) ON DELETE CASCADE;

CREATE FUNCTION protect_global_directory_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'directory roots retained' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id) OR EXISTS(SELECT 1 FROM orgs WHERE id=OLD.org_id) THEN
   RAISE EXCEPTION 'directory root retained until tenant deletion' USING ERRCODE='23514',CONSTRAINT='global_directory_identity';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP = 'UPDATE' AND (NEW.id,NEW.account_id,NEW.org_id) IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.org_id) THEN
  RAISE EXCEPTION 'directory owner identity immutable' USING ERRCODE='23514', CONSTRAINT='global_directory_identity';
 END IF;
 IF TG_OP='INSERT' AND NEW.org_id IS NOT NULL THEN
  PERFORM 1 FROM orgs WHERE id=NEW.org_id AND NOT personal_org AND status='active' AND NOT deleted_pending FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'shared organization inactive' USING ERRCODE='23514', CONSTRAINT='global_directory_owner_inactive'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_directory_owner_guard BEFORE INSERT OR UPDATE OR DELETE ON global_directory_owners FOR EACH ROW EXECUTE FUNCTION protect_global_directory_owner();
CREATE TRIGGER global_directory_owner_truncate BEFORE TRUNCATE ON global_directory_owners FOR EACH STATEMENT EXECUTE FUNCTION protect_global_directory_owner();

CREATE FUNCTION resolve_global_org_owner(wanted_org uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE root uuid;
BEGIN
 PERFORM assert_global_directory_scope();
 PERFORM 1 FROM orgs WHERE id=wanted_org AND NOT personal_org AND status='active' AND NOT deleted_pending FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'shared organization inactive' USING ERRCODE='23514', CONSTRAINT='global_directory_owner_inactive'; END IF;
 INSERT INTO global_directory_owners(org_id) VALUES(wanted_org) ON CONFLICT(org_id) DO NOTHING;
 SELECT id INTO root FROM global_directory_owners WHERE org_id=wanted_org;
 RETURN root;
END;
$$;

CREATE FUNCTION reject_shared_global_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM global_directory_owners WHERE id=NEW.owner_id AND account_id IS NOT NULL FOR SHARE;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'shared regional authorization not available' USING ERRCODE='23514', CONSTRAINT='global_directory_org_targets_unavailable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_owner_cell_binding_kind BEFORE INSERT OR UPDATE ON global_owner_cell_bindings FOR EACH ROW EXECUTE FUNCTION reject_shared_global_target();
CREATE TRIGGER global_app_target_owner_kind BEFORE INSERT OR UPDATE ON global_app_targets FOR EACH ROW EXECUTE FUNCTION reject_shared_global_target();
CREATE OR REPLACE FUNCTION protect_global_directory() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION guard_cell_from_global_directory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM global_directory_owners) OR EXISTS (SELECT 1 FROM global_directory_cells) OR EXISTS (SELECT 1 FROM global_apps) THEN
        RAISE EXCEPTION 'a populated global directory cannot become a regional cell'
            USING ERRCODE = '23514', CONSTRAINT = 'global_directory_scope';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION lock_global_release_target_app() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM 1 FROM global_apps WHERE id = OLD.global_app_id FOR UPDATE;
        RETURN OLD;
    END IF;
    PERFORM 1 FROM global_apps WHERE id = NEW.global_app_id FOR UPDATE;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION protect_global_image_release() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION enqueue_global_release_recovery(a uuid,o uuid,s text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 INSERT INTO global_release_recovery_work(global_app_id,owner_id,global_app_slug) VALUES(a,o,s)
 ON CONFLICT(global_app_id) DO UPDATE SET wake_id=gen_random_uuid(),next_attempt_at=clock_timestamp()
 WHERE global_release_recovery_work.owner_id=o AND global_release_recovery_work.global_app_slug=s;
END;
$$;

CREATE OR REPLACE FUNCTION protect_global_release_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner uuid; slug text; n bigint;
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN
  RAISE EXCEPTION 'delegation history retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.status NOT IN ('revoked','expired') THEN
   RAISE EXCEPTION 'revocation work retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
  RETURN OLD;
 END IF;
 SELECT owner_id,global_apps.slug INTO owner,slug FROM global_apps WHERE id=NEW.global_app_id FOR UPDATE;
 IF TG_OP='INSERT' THEN
  IF owner IS DISTINCT FROM NEW.owner_id OR slug IS DISTINCT FROM NEW.global_app_slug OR NEW.status<>'pending' THEN
   RAISE EXCEPTION 'current delegation owner required' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
  NEW.created_at=clock_timestamp();
  SELECT count(*) INTO n FROM global_release_delegations WHERE global_app_id=NEW.global_app_id AND status IN ('pending','active','revoking') AND expires_at>clock_timestamp();
  IF n>=1024 THEN RAISE EXCEPTION 'delegation bound' USING ERRCODE='23514',CONSTRAINT='global_delegation_limit',DETAIL=(n+1)::text; END IF;
 ELSE
  IF (NEW.id,NEW.global_app_id,NEW.owner_id,NEW.global_app_slug,NEW.cell_id,NEW.region,NEW.cell_generation,NEW.api_base,NEW.regional_account_id,NEW.regional_app_id,NEW.regional_app_slug,NEW.token_sha256,NEW.expires_at,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.global_app_id,OLD.owner_id,OLD.global_app_slug,OLD.cell_id,OLD.region,OLD.cell_generation,OLD.api_base,OLD.regional_account_id,OLD.regional_app_id,OLD.regional_app_slug,OLD.token_sha256,OLD.expires_at,OLD.created_at)
   OR (NEW.sealed_credential IS DISTINCT FROM OLD.sealed_credential AND NOT (NEW.status IN ('revoked','expired') AND NEW.sealed_credential IS NULL))
   OR (OLD.status IN ('revoked','expired') AND NEW.status<>OLD.status)
   OR (OLD.status='revoking' AND NEW.status IN ('pending','active')) OR (OLD.status='active' AND NEW.status='pending') THEN
   RAISE EXCEPTION 'delegation identity and retirement immutable' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
 END IF;
 IF NEW.status='expired' AND NEW.expires_at>clock_timestamp() THEN
  RAISE EXCEPTION 'expiry not reached' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 IF NEW.status IN ('pending','active') THEN
  IF owner IS DISTINCT FROM NEW.owner_id OR NEW.expires_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'delegation no longer usable' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell';
  END IF;
  PERFORM 1 FROM global_directory_cells WHERE id=NEW.cell_id AND enabled AND generation=NEW.cell_generation AND api_base=NEW.api_base FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'delegation cell changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
  PERFORM 1 FROM global_app_targets WHERE global_app_id=NEW.global_app_id AND region=NEW.region AND cell_id=NEW.cell_id AND cell_generation=NEW.cell_generation
   AND regional_account_id=NEW.regional_account_id AND regional_app_id=NEW.regional_app_id AND regional_app_slug=NEW.regional_app_slug;
  IF NOT FOUND THEN RAISE EXCEPTION 'delegation binding changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION wake_global_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='global_image_releases' THEN
  PERFORM enqueue_global_release_recovery(a.id,a.owner_id,a.slug) FROM global_apps a WHERE a.id=NEW.global_app_id;
 ELSE
  PERFORM enqueue_global_release_recovery(NEW.global_app_id,NEW.owner_id,NEW.global_app_slug);
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION retire_global_release_delegations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='global_apps' THEN
  UPDATE global_release_delegations SET status='revoking' WHERE global_app_id=OLD.id AND status IN ('pending','active');
 ELSIF TG_OP='DELETE' THEN
  UPDATE global_release_delegations SET status='revoking' WHERE global_app_id=OLD.global_app_id AND region=OLD.region AND status IN ('pending','active');
 ELSIF (NEW.cell_generation,NEW.cell_id,NEW.regional_account_id,NEW.regional_app_id,NEW.regional_app_slug)
   IS DISTINCT FROM (OLD.cell_generation,OLD.cell_id,OLD.regional_account_id,OLD.regional_app_id,OLD.regional_app_slug) THEN
  UPDATE global_release_delegations SET status='revoking' WHERE global_app_id=OLD.global_app_id AND region=OLD.region AND status IN ('pending','active');
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE OR REPLACE FUNCTION wake_retired_global_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='revoking' AND OLD.status<>NEW.status THEN
  PERFORM enqueue_global_release_recovery(NEW.global_app_id,NEW.owner_id,NEW.global_app_slug);
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION protect_global_recovery_work() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'recovery work retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity'; END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM global_release_delegations WHERE global_app_id=OLD.global_app_id AND status IN ('pending','active','revoking'))
   OR EXISTS(SELECT 1 FROM global_image_release_targets t JOIN global_apps a ON a.id=t.global_app_id JOIN global_image_releases r ON r.global_app_id=t.global_app_id AND r.release_id=t.release_id
     WHERE a.id=OLD.global_app_id AND r.generation=a.desired_release_generation AND t.outcome='pending') THEN
   RAISE EXCEPTION 'revocation work retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' AND NOT EXISTS(SELECT 1 FROM global_apps WHERE id=NEW.global_app_id AND owner_id=NEW.owner_id AND slug=NEW.global_app_slug)
   AND NOT EXISTS(SELECT 1 FROM global_release_delegations WHERE global_app_id=NEW.global_app_id AND owner_id=NEW.owner_id AND global_app_slug=NEW.global_app_slug) THEN
  RAISE EXCEPTION 'recovery owner required' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.global_app_id,NEW.owner_id,NEW.global_app_slug) IS DISTINCT FROM (OLD.global_app_id,OLD.owner_id,OLD.global_app_slug) THEN
  RAISE EXCEPTION 'recovery identity immutable' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION reject_regional_cell_with_delegations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM global_release_delegations) OR EXISTS(SELECT 1 FROM global_release_recovery_work) THEN
  RAISE EXCEPTION 'directory recovery state exists' USING ERRCODE='23514',CONSTRAINT='global_directory_scope';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION create_global_app(owner_id uuid, wanted_slug text, app_limit integer) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE existing global_apps%ROWTYPE; wanted_id uuid;
BEGIN
 PERFORM assert_global_directory_scope();
 -- Preserve the personal account UUID and lock order of ADR-476.
 PERFORM 1 FROM accounts WHERE id=create_global_app.owner_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'directory account missing' USING ERRCODE='23503'; END IF;
 INSERT INTO global_directory_owners(id,account_id) VALUES(create_global_app.owner_id,create_global_app.owner_id) ON CONFLICT(id) DO NOTHING;
 PERFORM 1 FROM global_directory_owners WHERE id=create_global_app.owner_id AND account_id=create_global_app.owner_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'owner kind conflicts' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 SELECT * INTO existing FROM global_apps WHERE slug=wanted_slug;
 IF FOUND THEN
  IF existing.owner_id <> create_global_app.owner_id THEN RAISE EXCEPTION 'global slug taken' USING ERRCODE='23505'; END IF;
  RETURN existing.id;
 END IF;
 IF app_limit<1 OR (SELECT count(*) FROM global_apps WHERE global_apps.owner_id=create_global_app.owner_id)>=app_limit THEN
  RAISE EXCEPTION 'global app identity limit reached' USING ERRCODE='23514',CONSTRAINT='global_directory_limit';
 END IF;
 INSERT INTO global_apps(owner_id,slug) VALUES(create_global_app.owner_id,wanted_slug) RETURNING id INTO wanted_id;
 RETURN wanted_id;
END;
$$;

CREATE FUNCTION create_global_org_app(wanted_owner uuid, actor uuid, wanted_slug text, expected_plan text, app_limit integer) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE tenant uuid; current_plan text; existing global_apps%ROWTYPE; wanted_id uuid;
BEGIN
 PERFORM assert_global_directory_scope();
 SELECT org_id INTO tenant FROM global_directory_owners WHERE id=wanted_owner;
 IF tenant IS NULL THEN RAISE EXCEPTION 'shared owner required' USING ERRCODE='23514',CONSTRAINT='global_directory_identity'; END IF;
 PERFORM 1 FROM accounts WHERE id=actor AND status='active' AND abuse_hold_at IS NULL AND email_verified_at IS NOT NULL FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'actor inactive' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_forbidden'; END IF;
 SELECT plan INTO current_plan FROM orgs WHERE id=tenant AND NOT personal_org AND status='active' AND NOT deleted_pending FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'organization inactive' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_inactive'; END IF;
 PERFORM 1 FROM org_memberships WHERE org_id=tenant AND account_id=actor AND removed_at IS NULL AND role IN ('owner','admin','developer') FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'organization role cannot create apps' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_forbidden'; END IF;
 IF current_plan<>expected_plan THEN RAISE EXCEPTION 'organization plan changed' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_plan_changed'; END IF;
 PERFORM 1 FROM global_directory_owners WHERE id=wanted_owner AND org_id=tenant FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'directory owner missing' USING ERRCODE='23503'; END IF;
 SELECT * INTO existing FROM global_apps WHERE slug=wanted_slug;
 IF FOUND THEN
  IF existing.owner_id<>wanted_owner THEN RAISE EXCEPTION 'global slug taken' USING ERRCODE='23505'; END IF;
  RETURN existing.id;
 END IF;
 IF app_limit<1 OR (SELECT count(*) FROM global_apps WHERE owner_id=wanted_owner)>=app_limit THEN
  RAISE EXCEPTION 'global app identity limit reached' USING ERRCODE='23514',CONSTRAINT='global_directory_limit';
 END IF;
 INSERT INTO global_apps(owner_id,slug) VALUES(wanted_owner,wanted_slug) RETURNING id INTO wanted_id;
 RETURN wanted_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM global_directory_owners WHERE org_id IS NOT NULL) THEN RAISE EXCEPTION 'shared directory owner downgrade refused'; END IF;
END $$;
DROP TRIGGER global_owner_cell_binding_kind ON global_owner_cell_bindings;
DROP TRIGGER global_app_target_owner_kind ON global_app_targets;
DROP FUNCTION reject_shared_global_target();
DROP FUNCTION create_global_org_app(uuid,uuid,text,text,integer);
DROP FUNCTION resolve_global_org_owner(uuid);
ALTER TABLE global_apps DROP CONSTRAINT global_apps_owner_id_fkey;
ALTER TABLE global_owner_cell_bindings DROP CONSTRAINT global_owner_cell_bindings_owner_id_fkey;
ALTER TABLE global_apps RENAME COLUMN owner_id TO account_id;
ALTER TABLE global_owner_cell_bindings RENAME COLUMN owner_id TO account_id;
ALTER TABLE global_owner_cell_bindings RENAME TO global_account_cell_bindings;
ALTER TABLE global_app_targets RENAME COLUMN owner_id TO account_id;
ALTER TABLE global_release_delegations RENAME COLUMN owner_id TO account_id;
ALTER TABLE global_release_recovery_work RENAME COLUMN owner_id TO account_id;
ALTER TABLE global_apps ADD CONSTRAINT global_apps_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE global_account_cell_bindings ADD CONSTRAINT global_account_cell_bindings_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
DROP TABLE global_directory_owners;
DROP FUNCTION protect_global_directory_owner();
CREATE OR REPLACE FUNCTION protect_global_directory() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION guard_cell_from_global_directory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM global_directory_cells) OR EXISTS (SELECT 1 FROM global_apps) THEN
        RAISE EXCEPTION 'a populated global directory cannot become a regional cell'
            USING ERRCODE = '23514', CONSTRAINT = 'global_directory_scope';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION create_global_app(owner_id uuid, wanted_slug text, app_limit integer) RETURNS uuid LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION lock_global_release_target_app() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM 1 FROM global_apps WHERE id = OLD.global_app_id FOR UPDATE;
        RETURN OLD;
    END IF;
    PERFORM 1 FROM global_apps WHERE id = NEW.global_app_id FOR UPDATE;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION protect_global_image_release() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION plan_global_image_release(owner_id uuid, wanted_slug text, wanted_release uuid, wanted_image text) RETURNS uuid LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION enqueue_global_release_recovery(a uuid,o uuid,s text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 INSERT INTO global_release_recovery_work(global_app_id,account_id,global_app_slug) VALUES(a,o,s)
 ON CONFLICT(global_app_id) DO UPDATE SET wake_id=gen_random_uuid(),next_attempt_at=clock_timestamp()
 WHERE global_release_recovery_work.account_id=o AND global_release_recovery_work.global_app_slug=s;
END;
$$;

CREATE OR REPLACE FUNCTION protect_global_release_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner uuid; slug text; n bigint;
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN
  RAISE EXCEPTION 'delegation history retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.status NOT IN ('revoked','expired') THEN
   RAISE EXCEPTION 'revocation work retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
  RETURN OLD;
 END IF;
 SELECT account_id,global_apps.slug INTO owner,slug FROM global_apps WHERE id=NEW.global_app_id FOR UPDATE;
 IF TG_OP='INSERT' THEN
  IF owner IS DISTINCT FROM NEW.account_id OR slug IS DISTINCT FROM NEW.global_app_slug OR NEW.status<>'pending' THEN
   RAISE EXCEPTION 'current delegation owner required' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
  NEW.created_at=clock_timestamp();
  SELECT count(*) INTO n FROM global_release_delegations WHERE global_app_id=NEW.global_app_id AND status IN ('pending','active','revoking') AND expires_at>clock_timestamp();
  IF n>=1024 THEN RAISE EXCEPTION 'delegation bound' USING ERRCODE='23514',CONSTRAINT='global_delegation_limit',DETAIL=(n+1)::text; END IF;
 ELSE
  IF (NEW.id,NEW.global_app_id,NEW.account_id,NEW.global_app_slug,NEW.cell_id,NEW.region,NEW.cell_generation,NEW.api_base,NEW.regional_account_id,NEW.regional_app_id,NEW.regional_app_slug,NEW.token_sha256,NEW.expires_at,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.global_app_id,OLD.account_id,OLD.global_app_slug,OLD.cell_id,OLD.region,OLD.cell_generation,OLD.api_base,OLD.regional_account_id,OLD.regional_app_id,OLD.regional_app_slug,OLD.token_sha256,OLD.expires_at,OLD.created_at)
   OR (NEW.sealed_credential IS DISTINCT FROM OLD.sealed_credential AND NOT (NEW.status IN ('revoked','expired') AND NEW.sealed_credential IS NULL))
   OR (OLD.status IN ('revoked','expired') AND NEW.status<>OLD.status)
   OR (OLD.status='revoking' AND NEW.status IN ('pending','active')) OR (OLD.status='active' AND NEW.status='pending') THEN
   RAISE EXCEPTION 'delegation identity and retirement immutable' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
 END IF;
 IF NEW.status='expired' AND NEW.expires_at>clock_timestamp() THEN
  RAISE EXCEPTION 'expiry not reached' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 IF NEW.status IN ('pending','active') THEN
  IF owner IS DISTINCT FROM NEW.account_id OR NEW.expires_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'delegation no longer usable' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell';
  END IF;
  PERFORM 1 FROM global_directory_cells WHERE id=NEW.cell_id AND enabled AND generation=NEW.cell_generation AND api_base=NEW.api_base FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'delegation cell changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
  PERFORM 1 FROM global_app_targets WHERE global_app_id=NEW.global_app_id AND region=NEW.region AND cell_id=NEW.cell_id AND cell_generation=NEW.cell_generation
   AND regional_account_id=NEW.regional_account_id AND regional_app_id=NEW.regional_app_id AND regional_app_slug=NEW.regional_app_slug;
  IF NOT FOUND THEN RAISE EXCEPTION 'delegation binding changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION wake_global_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='global_image_releases' THEN
  PERFORM enqueue_global_release_recovery(a.id,a.account_id,a.slug) FROM global_apps a WHERE a.id=NEW.global_app_id;
 ELSE
  PERFORM enqueue_global_release_recovery(NEW.global_app_id,NEW.account_id,NEW.global_app_slug);
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION retire_global_release_delegations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='global_apps' THEN
  UPDATE global_release_delegations SET status='revoking' WHERE global_app_id=OLD.id AND status IN ('pending','active');
 ELSIF TG_OP='DELETE' THEN
  UPDATE global_release_delegations SET status='revoking' WHERE global_app_id=OLD.global_app_id AND region=OLD.region AND status IN ('pending','active');
 ELSIF (NEW.cell_generation,NEW.cell_id,NEW.regional_account_id,NEW.regional_app_id,NEW.regional_app_slug)
   IS DISTINCT FROM (OLD.cell_generation,OLD.cell_id,OLD.regional_account_id,OLD.regional_app_id,OLD.regional_app_slug) THEN
  UPDATE global_release_delegations SET status='revoking' WHERE global_app_id=OLD.global_app_id AND region=OLD.region AND status IN ('pending','active');
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE OR REPLACE FUNCTION wake_retired_global_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='revoking' AND OLD.status<>NEW.status THEN
  PERFORM enqueue_global_release_recovery(NEW.global_app_id,NEW.account_id,NEW.global_app_slug);
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION protect_global_recovery_work() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'recovery work retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity'; END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM global_release_delegations WHERE global_app_id=OLD.global_app_id AND status IN ('pending','active','revoking'))
   OR EXISTS(SELECT 1 FROM global_image_release_targets t JOIN global_apps a ON a.id=t.global_app_id JOIN global_image_releases r ON r.global_app_id=t.global_app_id AND r.release_id=t.release_id
     WHERE a.id=OLD.global_app_id AND r.generation=a.desired_release_generation AND t.outcome='pending') THEN
   RAISE EXCEPTION 'revocation work retained' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' AND NOT EXISTS(SELECT 1 FROM global_apps WHERE id=NEW.global_app_id AND account_id=NEW.account_id AND slug=NEW.global_app_slug)
   AND NOT EXISTS(SELECT 1 FROM global_release_delegations WHERE global_app_id=NEW.global_app_id AND account_id=NEW.account_id AND global_app_slug=NEW.global_app_slug) THEN
  RAISE EXCEPTION 'recovery owner required' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.global_app_id,NEW.account_id,NEW.global_app_slug) IS DISTINCT FROM (OLD.global_app_id,OLD.account_id,OLD.global_app_slug) THEN
  RAISE EXCEPTION 'recovery identity immutable' USING ERRCODE='23514',CONSTRAINT='global_delegation_identity';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION reject_regional_cell_with_delegations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM global_release_delegations) OR EXISTS(SELECT 1 FROM global_release_recovery_work) THEN
  RAISE EXCEPTION 'directory recovery state exists' USING ERRCODE='23514',CONSTRAINT='global_directory_scope';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
