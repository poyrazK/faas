-- filename: 20261006192905057_global_runtime_delivery_consents.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-646: retained public runtime consent, independent of image delegation.
CREATE TABLE global_runtime_consents (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 issuer_id uuid NOT NULL CHECK(issuer_id<>'00000000-0000-0000-0000-000000000000'),
 global_app_id uuid NOT NULL CHECK(global_app_id<>'00000000-0000-0000-0000-000000000000'),
 owner_id uuid NOT NULL CHECK(owner_id<>'00000000-0000-0000-0000-000000000000'),
 global_app_slug text NOT NULL CHECK(length(global_app_slug) BETWEEN 3 AND 40 AND global_app_slug ~ '^[a-z0-9][a-z0-9-]*[a-z0-9]$'),
 binding jsonb NOT NULL,
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','revoking','revoked')),
 regional_created_at timestamptz CHECK(isfinite(regional_created_at) AND regional_created_at<expires_at AND regional_created_at<>'0001-01-01 00:00:00+00'::timestamptz),
 regional_revoked_at timestamptz CHECK(isfinite(regional_revoked_at)),
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '6 days'),
 CHECK((status='revoked')=(regional_revoked_at IS NOT NULL)),
 CHECK(status<>'active' OR regional_created_at IS NOT NULL),
 CHECK(status<>'pending' OR regional_created_at IS NULL),
 CHECK(regional_revoked_at IS NULL OR (regional_created_at IS NOT NULL AND regional_revoked_at>=regional_created_at)),
 CHECK((jsonb_typeof(binding)='object'
  AND binding ?& ARRAY['CellID','Region','CellGeneration','RegionalAccountID','RegionalOrgID','RegionalAppID','RegionalAppSlug','APIBase']
  AND binding-ARRAY['CellID','Region','CellGeneration','RegionalAccountID','RegionalOrgID','RegionalAppID','RegionalAppSlug','APIBase']='{}'::jsonb
  AND jsonb_typeof(binding->'CellID')='string' AND jsonb_typeof(binding->'Region')='string'
  AND jsonb_typeof(binding->'RegionalAccountID')='string' AND jsonb_typeof(binding->'RegionalOrgID')='string'
  AND jsonb_typeof(binding->'RegionalAppID')='string' AND jsonb_typeof(binding->'RegionalAppSlug')='string'
  AND jsonb_typeof(binding->'APIBase')='string' AND jsonb_typeof(binding->'CellGeneration')='number'
  AND binding->>'RegionalOrgID'='' AND binding->>'CellGeneration' ~ '^[1-9][0-9]*$'
  AND (binding->>'CellGeneration')::numeric<=9007199254740991
  AND binding->>'CellID' ~ '^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$'
  AND binding->>'RegionalAccountID' ~ '^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$'
  AND binding->>'RegionalAppID' ~ '^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$'
  AND binding->>'CellID'<>'00000000-0000-0000-0000-000000000000'
  AND binding->>'RegionalAccountID'<>'00000000-0000-0000-0000-000000000000'
  AND binding->>'RegionalAppID'<>'00000000-0000-0000-0000-000000000000'
  AND length(binding->>'RegionalAppSlug') BETWEEN 3 AND 40
  AND binding->>'RegionalAppSlug' ~ '^[a-z0-9][a-z0-9-]*[a-z0-9]$'
  AND octet_length(binding->>'APIBase')<=2048
  AND binding->>'APIBase' ~ '^https://([a-z0-9.-]+|\[[0-9a-f:]+\])(:[0-9]{1,5})?$') IS TRUE)
);
CREATE INDEX global_runtime_consents_app ON global_runtime_consents(global_app_id,status,expires_at);

CREATE FUNCTION check_global_runtime_consent_scope(wanted_owner uuid,wanted_app uuid,wanted_slug text,wanted_issuer uuid,b jsonb,expiry timestamptz) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM lock_global_runtime_quota_tenant(wanted_owner);
 PERFORM 1 FROM global_directory_owners WHERE id=wanted_owner AND account_id=wanted_owner AND org_id IS NULL;
 IF NOT FOUND THEN RAISE EXCEPTION 'personal runtime consent owner required' USING ERRCODE='23514',CONSTRAINT='global_directory_org_targets_unavailable'; END IF;
 PERFORM 1 FROM global_runtime_quota_issuer WHERE issuer_id=wanted_issuer;
 IF NOT FOUND THEN RAISE EXCEPTION 'runtime consent issuer differs' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_identity'; END IF;
 PERFORM 1 FROM global_apps WHERE id=wanted_app AND owner_id=wanted_owner AND slug=wanted_slug FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'runtime consent app unavailable' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_unavailable'; END IF;
 PERFORM 1 FROM global_directory_cells WHERE id=(b->>'CellID')::uuid AND region=b->>'Region'
  AND api_base=b->>'APIBase' AND generation=(b->>'CellGeneration')::bigint AND enabled FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'runtime consent registry changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
 PERFORM 1 FROM global_app_targets WHERE global_app_id=wanted_app AND region=b->>'Region'
  AND cell_id=(b->>'CellID')::uuid AND cell_generation=(b->>'CellGeneration')::bigint
  AND regional_account_id=(b->>'RegionalAccountID')::uuid AND regional_org_id IS NULL
  AND regional_app_id=(b->>'RegionalAppID')::uuid AND regional_app_slug=b->>'RegionalAppSlug'
  AND b->>'RegionalOrgID'='' FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'runtime consent target changed' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
 -- All owner/app/cell/target waits precede the authoritative deadline check.
 IF expiry<=clock_timestamp() THEN RAISE EXCEPTION 'runtime consent expired' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_unavailable'; END IF;
END; $$;

CREATE FUNCTION guard_global_runtime_consent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'runtime consent evidence is retained' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_identity'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('gregale-global-runtime-consent:'||NEW.global_app_id::text,0));
 IF TG_OP='INSERT' THEN
  NEW.created_at:=clock_timestamp();
  IF NEW.status<>'pending' OR NEW.regional_created_at IS NOT NULL OR NEW.regional_revoked_at IS NOT NULL THEN RAISE EXCEPTION 'new runtime consent must be pending' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_identity'; END IF;
  PERFORM check_global_runtime_consent_scope(NEW.owner_id,NEW.global_app_id,NEW.global_app_slug,NEW.issuer_id,NEW.binding,NEW.expires_at);
  NEW.created_at:=clock_timestamp();
  IF (SELECT count(*) FROM global_runtime_consents WHERE global_app_id=NEW.global_app_id AND status<>'revoked' AND expires_at>clock_timestamp())>=32 THEN RAISE EXCEPTION 'runtime consent bound reached' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_limit'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.issuer_id,NEW.global_app_id,NEW.owner_id,NEW.global_app_slug,NEW.binding,NEW.expires_at,NEW.created_at)
   IS DISTINCT FROM ROW(OLD.id,OLD.issuer_id,OLD.global_app_id,OLD.owner_id,OLD.global_app_slug,OLD.binding,OLD.expires_at,OLD.created_at)
   OR (OLD.regional_created_at IS NOT NULL AND NEW.regional_created_at IS DISTINCT FROM OLD.regional_created_at)
   OR (OLD.regional_revoked_at IS NOT NULL AND NEW.regional_revoked_at IS DISTINCT FROM OLD.regional_revoked_at)
   OR (OLD.status='revoked' AND NEW.status<>'revoked') OR (OLD.status='revoking' AND NEW.status NOT IN ('revoking','revoked'))
   OR (OLD.status='active' AND NEW.status='pending') THEN RAISE EXCEPTION 'runtime consent original identity is immutable' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_identity'; END IF;
  IF NEW.status='active' THEN PERFORM check_global_runtime_consent_scope(NEW.owner_id,NEW.global_app_id,NEW.global_app_slug,NEW.issuer_id,NEW.binding,NEW.expires_at); END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER global_runtime_consent_guard BEFORE INSERT OR UPDATE OR DELETE ON global_runtime_consents FOR EACH ROW EXECUTE FUNCTION guard_global_runtime_consent();
CREATE TRIGGER global_runtime_consent_truncate_guard BEFORE TRUNCATE ON global_runtime_consents FOR EACH STATEMENT EXECUTE FUNCTION guard_global_runtime_consent();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE global_runtime_consents IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM global_runtime_consents) THEN RAISE EXCEPTION 'cannot downgrade retained directory runtime consent'; END IF;
END; $$;
DROP TABLE global_runtime_consents;
DROP FUNCTION guard_global_runtime_consent();
DROP FUNCTION check_global_runtime_consent_scope(uuid,uuid,text,uuid,jsonb,timestamptz);
-- +goose StatementEnd
