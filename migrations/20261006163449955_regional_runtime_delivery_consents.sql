-- ADR-645: revocable personal consent for credential-free directory quota delivery.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE regional_runtime_consents (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    app_id uuid NOT NULL CHECK (app_id <> '00000000-0000-0000-0000-000000000000'),
    account_id uuid NOT NULL CHECK (account_id <> '00000000-0000-0000-0000-000000000000'),
    cell_id uuid NOT NULL REFERENCES control_plane_cells(id),
    issuer_id uuid NOT NULL CHECK (issuer_id <> '00000000-0000-0000-0000-000000000000'),
    global_app_id uuid NOT NULL CHECK (global_app_id <> '00000000-0000-0000-0000-000000000000'),
    owner_id uuid NOT NULL CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'),
    binding jsonb NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    revoked_at timestamptz,
    CHECK ((status='active' AND revoked_at IS NULL) OR (status='revoked' AND revoked_at IS NOT NULL AND revoked_at>=created_at)),
    CHECK (expires_at>created_at AND expires_at<=created_at+interval '6 days'),
    CHECK ((jsonb_typeof(binding)='object'
      AND binding ?& ARRAY['CellID','Region','CellGeneration','RegionalAccountID','RegionalOrgID','RegionalAppID','RegionalAppSlug','APIBase']
      AND binding - ARRAY['CellID','Region','CellGeneration','RegionalAccountID','RegionalOrgID','RegionalAppID','RegionalAppSlug','APIBase']='{}'::jsonb
      AND jsonb_typeof(binding->'CellID')='string' AND jsonb_typeof(binding->'Region')='string'
      AND jsonb_typeof(binding->'RegionalAccountID')='string' AND jsonb_typeof(binding->'RegionalOrgID')='string'
      AND jsonb_typeof(binding->'RegionalAppID')='string' AND jsonb_typeof(binding->'RegionalAppSlug')='string'
      AND jsonb_typeof(binding->'APIBase')='string'
      AND jsonb_typeof(binding->'CellGeneration')='number' AND binding->>'CellID'=cell_id::text
      AND binding->>'RegionalAppID'=app_id::text AND binding->>'RegionalAccountID'=account_id::text
      AND binding->>'RegionalOrgID'='' AND binding->>'CellGeneration' ~ '^[1-9][0-9]*$'
      AND (binding->>'CellGeneration')::numeric<=9007199254740991
      AND binding->>'APIBase' ~ '^https://([a-z0-9.-]+|\[[0-9a-f:]+\])(:[0-9]{1,5})?$') IS TRUE)
);
CREATE INDEX regional_runtime_consents_app ON regional_runtime_consents(app_id,status,expires_at);

CREATE FUNCTION lock_regional_runtime_consent_owner(binding_arg jsonb) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    PERFORM lock_regional_runtime_quota_app_binding(binding_arg);
    PERFORM 1 FROM apps a JOIN control_plane_cells c ON c.id=(binding_arg->>'CellID')::uuid
      WHERE a.id=(binding_arg->>'RegionalAppID')::uuid AND a.compute_region=binding_arg->>'Region'
        AND c.compute_region=a.compute_region;
    IF NOT FOUND THEN RAISE EXCEPTION 'runtime consent cell or region is unavailable' USING ERRCODE='P0002'; END IF;
    PERFORM 1 FROM accounts WHERE id=(binding_arg->>'RegionalAccountID')::uuid AND status='active'
      AND abuse_hold_at IS NULL AND email_verified_at IS NOT NULL FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'runtime consent owner is unavailable' USING ERRCODE='P0002'; END IF;
END; $$;

CREATE FUNCTION guard_regional_runtime_consent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
      RAISE EXCEPTION 'retained runtime consent cannot be removed' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_identity';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
    IF TG_OP='INSERT' THEN
      NEW.created_at:=clock_timestamp();
      IF NEW.status<>'active' OR NEW.revoked_at IS NOT NULL OR NEW.expires_at<=NEW.created_at THEN
        RAISE EXCEPTION 'invalid new runtime consent' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_identity';
      END IF;
      PERFORM lock_regional_runtime_consent_owner(NEW.binding);
      IF (SELECT count(*) FROM regional_runtime_consents WHERE app_id=NEW.app_id AND status='active' AND expires_at>clock_timestamp())>=32 THEN
        RAISE EXCEPTION 'active runtime consent bound reached' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_limit';
      END IF;
    ELSE
      IF ROW(NEW.id,NEW.app_id,NEW.account_id,NEW.cell_id,NEW.issuer_id,NEW.global_app_id,NEW.owner_id,NEW.binding,NEW.expires_at,NEW.created_at)
        IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.account_id,OLD.cell_id,OLD.issuer_id,OLD.global_app_id,OLD.owner_id,OLD.binding,OLD.expires_at,OLD.created_at)
        OR OLD.status='revoked' AND ROW(NEW.status,NEW.revoked_at) IS DISTINCT FROM ROW(OLD.status,OLD.revoked_at) THEN
        RAISE EXCEPTION 'runtime consent identity is immutable' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_identity';
      END IF;
      IF NEW.status='revoked' THEN
        UPDATE regional_runtime_quota_heads SET admission_enabled=false WHERE app_id=NEW.app_id AND admission_enabled;
      END IF;
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER regional_runtime_consent_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_consents
    FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_consent();
CREATE TRIGGER regional_runtime_consent_truncate_guard BEFORE TRUNCATE ON regional_runtime_consents
    FOR EACH STATEMENT EXECUTE FUNCTION guard_regional_runtime_consent();

ALTER TABLE regional_runtime_quota_receipts ADD COLUMN consent_id uuid REFERENCES regional_runtime_consents(id);
CREATE FUNCTION guard_regional_runtime_quota_consent_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE consent_expiry timestamptz;
BEGIN
    IF TG_OP='UPDATE' THEN
      IF NEW.consent_id IS DISTINCT FROM OLD.consent_id THEN
        RAISE EXCEPTION 'original quota consent source is immutable' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_identity';
      END IF;
    ELSIF NEW.consent_id IS NOT NULL THEN
      PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
      SELECT c.expires_at INTO consent_expiry FROM regional_runtime_consents c WHERE c.id=NEW.consent_id AND c.app_id=NEW.app_id
        AND c.status='active' AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp()
        AND c.issuer_id=(NEW.grant_claims->>'IssuerID')::uuid AND c.global_app_id=(NEW.grant_claims->>'GlobalAppID')::uuid
        AND c.owner_id=(NEW.grant_claims->>'OwnerID')::uuid AND c.binding=NEW.grant_claims->'Binding';
      IF NOT FOUND OR NEW.grant_envelope IS NULL OR NEW.retirement IS NOT NULL THEN
        RAISE EXCEPTION 'quota delivery consent is unavailable' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_unavailable';
      END IF;
      PERFORM lock_regional_runtime_consent_owner(NEW.grant_claims->'Binding');
      IF consent_expiry<=clock_timestamp() THEN
        RAISE EXCEPTION 'quota delivery consent expired while waiting for owner' USING ERRCODE='23514',CONSTRAINT='regional_runtime_consent_unavailable';
      END IF;
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER regional_runtime_quota_consent_guard BEFORE INSERT OR UPDATE ON regional_runtime_quota_receipts
    FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_quota_consent_source();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM regional_runtime_consents) THEN
        RAISE EXCEPTION 'cannot downgrade retained runtime consents';
    END IF;
END $$;
DROP TRIGGER regional_runtime_quota_consent_guard ON regional_runtime_quota_receipts;
DROP FUNCTION guard_regional_runtime_quota_consent_source();
ALTER TABLE regional_runtime_quota_receipts DROP COLUMN consent_id;
DROP TABLE regional_runtime_consents;
DROP FUNCTION guard_regional_runtime_consent();
DROP FUNCTION lock_regional_runtime_consent_owner(jsonb);
-- +goose StatementEnd
