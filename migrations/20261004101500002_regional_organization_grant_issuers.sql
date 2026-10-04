-- ADR-484. Regional ownership remains pinned; consent has its own live actor.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE regional_release_grants ADD COLUMN issuer_account_id uuid;
UPDATE regional_release_grants SET issuer_account_id=account_id;
ALTER TABLE regional_release_grants ALTER COLUMN issuer_account_id SET NOT NULL;
ALTER TABLE regional_release_grants ADD CONSTRAINT regional_grant_issuer_nonzero
    CHECK (issuer_account_id <> '00000000-0000-0000-0000-000000000000');
-- Issuer IDs are historical attribution, deliberately without a cascading FK.
CREATE FUNCTION regional_release_actor_allowed(app_id_arg uuid,owner_arg uuid,org_arg uuid,actor_arg uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT EXISTS(SELECT 1 FROM apps a WHERE a.id=app_id_arg AND a.account_id=owner_arg
        AND a.org_id IS NOT DISTINCT FROM org_arg AND a.status IN ('active','evicted_cold')
        AND ((a.org_id IS NULL AND actor_arg=owner_arg) OR EXISTS(SELECT 1 FROM orgs o
            WHERE o.id=a.org_id AND o.status='active' AND NOT o.deleted_pending
            AND ((o.personal_org AND o.personal_owner_account_id=owner_arg AND actor_arg=owner_arg)
                OR (NOT o.personal_org AND EXISTS(SELECT 1 FROM accounts issuer JOIN org_memberships m
                    ON m.account_id=issuer.id AND m.org_id=o.id
                    WHERE issuer.id=actor_arg AND issuer.status='active' AND issuer.abuse_hold_at IS NULL
                    AND issuer.email_verified_at IS NOT NULL AND m.removed_at IS NULL AND m.role IN ('owner','admin')))))))
$$;

-- Lock order stays app before grant. Shared organization, account and membership
-- rows remain locked through registration/revocation so authority cannot change
-- between the check and commit. The function is volatile to refresh snapshots
-- after a lock wait.
CREATE FUNCTION lock_regional_release_grant_app(app_id_arg uuid,cell_arg uuid,owner_arg uuid,actor_arg uuid)
RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE org uuid; pinned_owner uuid;
BEGIN
    SELECT a.org_id,a.account_id INTO org,pinned_owner FROM apps a JOIN control_plane_cells c ON c.id=cell_arg
        WHERE a.id=app_id_arg AND (owner_arg IS NULL OR a.account_id=owner_arg) AND a.status IN ('active','evicted_cold') FOR UPDATE OF a;
    IF NOT FOUND THEN RAISE EXCEPTION 'regional grant app unavailable' USING ERRCODE='P0002'; END IF;
    IF org IS NOT NULL THEN
        PERFORM 1 FROM orgs WHERE id=org FOR SHARE;
        PERFORM 1 FROM accounts WHERE id=actor_arg FOR SHARE;
        PERFORM 1 FROM org_memberships WHERE org_id=org AND account_id=actor_arg FOR SHARE;
    END IF;
    IF NOT regional_release_actor_allowed(app_id_arg,pinned_owner,org,actor_arg) THEN
        RAISE EXCEPTION 'regional grant actor unavailable' USING ERRCODE='P0002';
    END IF;
    RETURN org;
END;
$$;

CREATE OR REPLACE FUNCTION protect_regional_release_grant() RETURNS trigger LANGUAGE plpgsql AS $$
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
        IF ROW(NEW.id,NEW.app_id,NEW.cell_id,NEW.account_id,NEW.issuer_account_id,NEW.org_id,NEW.global_app_id,NEW.token_sha256,NEW.created_at,NEW.expires_at)
           IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.cell_id,OLD.account_id,OLD.issuer_account_id,OLD.org_id,OLD.global_app_id,OLD.token_sha256,OLD.created_at,OLD.expires_at)
           OR (OLD.status='revoked' AND ROW(NEW.status,NEW.revoked_at) IS DISTINCT FROM ROW(OLD.status,OLD.revoked_at)) THEN
            RAISE EXCEPTION 'regional grant identity or revoked state changed' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity';
        END IF;
        RETURN NEW;
    END IF;
    NEW.created_at := clock_timestamp();
    IF NEW.status<>'active' OR NEW.revoked_at IS NOT NULL THEN RAISE EXCEPTION 'new grant must be active' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity'; END IF;
    PERFORM lock_regional_release_grant_app(NEW.app_id,NEW.cell_id,NEW.account_id,NEW.issuer_account_id);
    IF EXISTS(SELECT 1 FROM regional_image_release_heads h WHERE h.app_id=NEW.app_id
          AND (h.cell_id<>NEW.cell_id OR h.account_id<>NEW.account_id OR h.global_app_id<>NEW.global_app_id)) THEN
        RAISE EXCEPTION 'regional grant owner or stream mismatch' USING ERRCODE='23514',CONSTRAINT='regional_grant_identity';
    END IF;
    SELECT count(*)+1 INTO observed FROM regional_release_grants WHERE app_id=NEW.app_id AND status='active' AND expires_at>clock_timestamp();
    IF observed>32 THEN RAISE EXCEPTION 'regional active grant bound reached' USING ERRCODE='23514',CONSTRAINT='regional_grant_limit',DETAIL=observed::text; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM regional_release_grants g LEFT JOIN orgs o ON o.id=g.org_id
        WHERE g.issuer_account_id<>g.account_id OR (g.org_id IS NOT NULL AND NOT coalesce(o.personal_org,false))) THEN
        RAISE EXCEPTION 'cannot downgrade organization regional grants';
    END IF;
END $$;
CREATE OR REPLACE FUNCTION protect_regional_release_grant() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP FUNCTION lock_regional_release_grant_app(uuid,uuid,uuid,uuid);
DROP FUNCTION regional_release_actor_allowed(uuid,uuid,uuid,uuid);
ALTER TABLE regional_release_grants DROP COLUMN issuer_account_id;
-- +goose StatementEnd
