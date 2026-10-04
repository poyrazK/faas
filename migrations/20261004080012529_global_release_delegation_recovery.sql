-- ADR-482. Protocol bounds mirror pkg/api/limits.go.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE global_release_delegations (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 global_app_id uuid NOT NULL CHECK(global_app_id<>'00000000-0000-0000-0000-000000000000'),
 account_id uuid NOT NULL CHECK(account_id<>'00000000-0000-0000-0000-000000000000'),
 global_app_slug text NOT NULL CHECK(length(global_app_slug) BETWEEN 3 AND 40),
 cell_id uuid NOT NULL CHECK(cell_id<>'00000000-0000-0000-0000-000000000000'),
 region text NOT NULL CHECK(length(region) BETWEEN 1 AND 63 AND region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
 cell_generation bigint NOT NULL CHECK(cell_generation>0),
 api_base text NOT NULL CHECK(length(api_base)<=2048 AND api_base ~ '^https://([a-z0-9.-]+|\[[0-9a-f:]+\])(:[0-9]{1,5})?$'),
 regional_account_id uuid NOT NULL CHECK(regional_account_id<>'00000000-0000-0000-0000-000000000000'),
 regional_app_id uuid NOT NULL CHECK(regional_app_id<>'00000000-0000-0000-0000-000000000000'),
 regional_app_slug text NOT NULL CHECK(length(regional_app_slug) BETWEEN 3 AND 40),
 token_sha256 bytea NOT NULL CHECK(octet_length(token_sha256)=32),
 sealed_credential bytea,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','revoking','revoked','expired')),
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '7 days'),
 CHECK((status IN ('pending','active','revoking') AND sealed_credential IS NOT NULL AND octet_length(sealed_credential) BETWEEN 1 AND 4096
   AND substring(sealed_credential FROM 1 FOR 22)=convert_to('age-encryption.org/v1'||chr(10),'UTF8'))
   OR (status IN ('revoked','expired') AND sealed_credential IS NULL))
);
-- These snapshots deliberately outlive global-app/account deletion for revocation.
CREATE UNIQUE INDEX global_delegation_current ON global_release_delegations(global_app_id,region) WHERE status IN ('pending','active');
CREATE INDEX global_delegation_recovery ON global_release_delegations(global_app_id,status,created_at);
CREATE TABLE global_release_recovery_work (
 global_app_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 global_app_slug text NOT NULL,
 wake_id uuid NOT NULL DEFAULT gen_random_uuid(),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 claim_id uuid,
 claim_until timestamptz,
 cursor_id uuid,
 CHECK((claim_id IS NULL AND claim_until IS NULL) OR (claim_id IS NOT NULL AND claim_until IS NOT NULL)),
 CHECK(global_app_id<>'00000000-0000-0000-0000-000000000000' AND account_id<>'00000000-0000-0000-0000-000000000000'),
 CHECK(length(global_app_slug) BETWEEN 3 AND 40)
);
CREATE INDEX global_recovery_due ON global_release_recovery_work(next_attempt_at,claim_until);
CREATE FUNCTION enqueue_global_release_recovery(a uuid,o uuid,s text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 INSERT INTO global_release_recovery_work(global_app_id,account_id,global_app_slug) VALUES(a,o,s)
 ON CONFLICT(global_app_id) DO UPDATE SET wake_id=gen_random_uuid(),next_attempt_at=clock_timestamp()
 WHERE global_release_recovery_work.account_id=o AND global_release_recovery_work.global_app_slug=s;
END;
$$;
CREATE FUNCTION protect_global_release_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER global_delegation_guard BEFORE INSERT OR UPDATE OR DELETE ON global_release_delegations FOR EACH ROW EXECUTE FUNCTION protect_global_release_delegation();
CREATE TRIGGER global_delegation_truncate BEFORE TRUNCATE ON global_release_delegations FOR EACH STATEMENT EXECUTE FUNCTION protect_global_release_delegation();
CREATE FUNCTION wake_global_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='global_image_releases' THEN
  PERFORM enqueue_global_release_recovery(a.id,a.account_id,a.slug) FROM global_apps a WHERE a.id=NEW.global_app_id;
 ELSE
  PERFORM enqueue_global_release_recovery(NEW.global_app_id,NEW.account_id,NEW.global_app_slug);
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_delegation_wakeup AFTER INSERT ON global_release_delegations FOR EACH ROW EXECUTE FUNCTION wake_global_delegation();
CREATE TRIGGER global_image_release_wakeup AFTER INSERT ON global_image_releases FOR EACH ROW EXECUTE FUNCTION wake_global_delegation();
CREATE FUNCTION retire_global_release_delegations() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER global_app_delegation_retirement BEFORE DELETE ON global_apps FOR EACH ROW EXECUTE FUNCTION retire_global_release_delegations();
-- Locks from ADR-480 precede this trigger; retirement is atomic with target changes.
CREATE TRIGGER global_target_delegation_retirement BEFORE UPDATE OR DELETE ON global_app_targets FOR EACH ROW EXECUTE FUNCTION retire_global_release_delegations();
CREATE FUNCTION wake_retired_global_delegation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='revoking' AND OLD.status<>NEW.status THEN
  PERFORM enqueue_global_release_recovery(NEW.global_app_id,NEW.account_id,NEW.global_app_slug);
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_delegation_retirement_wakeup AFTER UPDATE ON global_release_delegations FOR EACH ROW EXECUTE FUNCTION wake_retired_global_delegation();
CREATE FUNCTION protect_global_recovery_work() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER global_recovery_guard BEFORE INSERT OR UPDATE OR DELETE ON global_release_recovery_work FOR EACH ROW EXECUTE FUNCTION protect_global_recovery_work();
CREATE TRIGGER global_recovery_truncate BEFORE TRUNCATE ON global_release_recovery_work FOR EACH STATEMENT EXECUTE FUNCTION protect_global_recovery_work();
CREATE FUNCTION reject_regional_cell_with_delegations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM global_release_delegations) OR EXISTS(SELECT 1 FROM global_release_recovery_work) THEN
  RAISE EXCEPTION 'directory recovery state exists' USING ERRCODE='23514',CONSTRAINT='global_directory_scope';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER regional_cell_delegation_scope BEFORE INSERT OR UPDATE ON control_plane_cells FOR EACH ROW EXECUTE FUNCTION reject_regional_cell_with_delegations();
INSERT INTO global_release_recovery_work(global_app_id,account_id,global_app_slug)
 SELECT a.id,a.account_id,a.slug FROM global_apps a WHERE EXISTS(SELECT 1 FROM global_image_releases r WHERE r.global_app_id=a.id AND r.generation=a.desired_release_generation);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM global_release_delegations) OR EXISTS(SELECT 1 FROM global_release_recovery_work) THEN RAISE EXCEPTION 'populated delegation recovery downgrade refused'; END IF;
END; $$;
DROP TRIGGER regional_cell_delegation_scope ON control_plane_cells;
DROP FUNCTION reject_regional_cell_with_delegations();
DROP TRIGGER global_image_release_wakeup ON global_image_releases;
DROP TRIGGER global_app_delegation_retirement ON global_apps;
DROP TRIGGER global_target_delegation_retirement ON global_app_targets;
DROP TABLE global_release_delegations;
DROP TABLE global_release_recovery_work;
DROP FUNCTION protect_global_release_delegation();
DROP FUNCTION wake_global_delegation();
DROP FUNCTION retire_global_release_delegations();
DROP FUNCTION wake_retired_global_delegation();
DROP FUNCTION protect_global_recovery_work();
DROP FUNCTION enqueue_global_release_recovery(uuid,uuid,text);
-- +goose StatementEnd
