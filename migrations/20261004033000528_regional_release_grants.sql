-- ADR-481. Protocol bounds mirror pkg/api/limits.go.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE regional_release_grants (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    cell_id uuid NOT NULL CHECK (cell_id <> '00000000-0000-0000-0000-000000000000'),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    org_id uuid REFERENCES orgs(id) ON DELETE CASCADE,
    global_app_id uuid NOT NULL CHECK (global_app_id <> '00000000-0000-0000-0000-000000000000'),
    token_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(token_sha256)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL CHECK (expires_at>created_at AND expires_at<=created_at+interval '7 days'),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    revoked_at timestamptz,
    CHECK ((status='active' AND revoked_at IS NULL) OR (status='revoked' AND revoked_at IS NOT NULL))
);
CREATE INDEX regional_release_grants_app_active ON regional_release_grants(app_id,expires_at) WHERE status='active';

CREATE FUNCTION protect_regional_release_grant() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE observed bigint;
BEGIN
    IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'regional grant tombstones are retained' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity'; END IF;
    IF TG_OP='DELETE' THEN
        IF EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) AND EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id)
           AND (OLD.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs WHERE id=OLD.org_id)) THEN
            RAISE EXCEPTION 'regional grant tombstones are retained' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity';
        END IF;
        RETURN OLD;
    END IF;
    PERFORM 1 FROM apps WHERE id=NEW.app_id FOR UPDATE;
    IF TG_OP='UPDATE' THEN
        IF ROW(NEW.id,NEW.app_id,NEW.cell_id,NEW.account_id,NEW.org_id,NEW.global_app_id,NEW.token_sha256,NEW.created_at,NEW.expires_at)
           IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.cell_id,OLD.account_id,OLD.org_id,OLD.global_app_id,OLD.token_sha256,OLD.created_at,OLD.expires_at)
           OR (OLD.status='revoked' AND ROW(NEW.status,NEW.revoked_at) IS DISTINCT FROM ROW(OLD.status,OLD.revoked_at)) THEN
            RAISE EXCEPTION 'regional grant identity or revoked state changed' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity';
        END IF;
        RETURN NEW;
    END IF;
    NEW.created_at := clock_timestamp();
    IF NEW.status<>'active' OR NEW.revoked_at IS NOT NULL THEN RAISE EXCEPTION 'new grant must be active' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity'; END IF;
    IF NOT EXISTS(SELECT 1 FROM apps a JOIN control_plane_cells c ON c.id=NEW.cell_id
        WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id AND a.org_id IS NOT DISTINCT FROM NEW.org_id
          AND a.status IN ('active','evicted_cold') AND (a.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o
          WHERE o.id=a.org_id AND o.personal_org AND o.personal_owner_account_id=a.account_id AND o.status='active' AND NOT o.deleted_pending)))
       OR EXISTS(SELECT 1 FROM regional_image_release_heads h WHERE h.app_id=NEW.app_id
          AND (h.cell_id<>NEW.cell_id OR h.account_id<>NEW.account_id OR h.global_app_id<>NEW.global_app_id)) THEN
        RAISE EXCEPTION 'regional grant owner or stream mismatch' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity';
    END IF;
    SELECT count(*)+1 INTO observed FROM regional_release_grants WHERE app_id=NEW.app_id AND status='active' AND expires_at>clock_timestamp();
    IF observed>32 THEN RAISE EXCEPTION 'regional active grant bound reached' USING ERRCODE='23514',CONSTRAINT='regional_grant_limit',DETAIL=observed::text; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_release_grants_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_release_grants FOR EACH ROW EXECUTE FUNCTION protect_regional_release_grant();
CREATE TRIGGER regional_release_grants_truncate_guard BEFORE TRUNCATE ON regional_release_grants FOR EACH STATEMENT EXECUTE FUNCTION protect_regional_release_grant();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM regional_release_grants) THEN RAISE EXCEPTION 'cannot downgrade populated regional grants'; END IF; END $$;
DROP TABLE regional_release_grants;
DROP FUNCTION protect_regional_release_grant();
-- +goose StatementEnd
