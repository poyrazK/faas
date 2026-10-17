-- Organization employee offboarding and transaction fencing (ADR-202610171902).
-- +goose Up
-- Replay completed expansion without replacing functions refined by later migrations.
-- A partially present expansion still fails atomically and requires repair.
-- +goose StatementBegin
DO $org_ownership_replay$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('org_memberships') AND attname='access_revision' AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('org_memberships') AND conname='org_memberships_access_revision_chk')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='advance_org_access_revision')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='revoke_removed_org_member_credentials')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='check_org_credential_membership')
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('org_memberships') AND tgname='org_membership_access_revision' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('org_memberships') AND tgname='org_member_credentials_revoked' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('api_keys') AND tgname='org_api_key_membership' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('deploy_tokens') AND tgname='org_deploy_token_membership' AND NOT tgisinternal) THEN
  RETURN;
 END IF;

ALTER TABLE org_memberships ADD COLUMN access_revision bigserial NOT NULL;
ALTER TABLE org_memberships ADD CONSTRAINT org_memberships_access_revision_chk CHECK (access_revision > 0);

CREATE FUNCTION advance_org_access_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.role IS DISTINCT FROM OLD.role
       OR NEW.removed_at IS DISTINCT FROM OLD.removed_at
       OR NEW.joined_at IS DISTINCT FROM OLD.joined_at THEN
        NEW.access_revision := nextval('org_memberships_access_revision_seq');
    ELSE
        NEW.access_revision := OLD.access_revision;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER org_membership_access_revision
BEFORE UPDATE ON org_memberships
FOR EACH ROW EXECUTE FUNCTION advance_org_access_revision();

CREATE FUNCTION revoke_removed_org_member_credentials() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.removed_at IS NOT NULL AND OLD.removed_at IS NULL THEN
        UPDATE api_keys SET status = 'revoked', revoked_at = coalesce(revoked_at, now())
        WHERE org_id = NEW.org_id AND account_id = NEW.account_id AND status <> 'revoked';
        UPDATE deploy_tokens t SET status = 'revoked', revoked_at = coalesce(t.revoked_at, now())
        FROM apps a WHERE t.app_id = a.id AND a.org_id = NEW.org_id
          AND t.account_id = NEW.account_id AND t.status <> 'revoked';
        UPDATE org_invitations SET revoked_at = coalesce(revoked_at, now())
        WHERE org_id = NEW.org_id AND consumed_at IS NULL AND revoked_at IS NULL
          AND lower(email::text) = (SELECT lower(email::text) FROM accounts WHERE id = NEW.account_id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER org_member_credentials_revoked
AFTER UPDATE OF removed_at ON org_memberships
FOR EACH ROW EXECUTE FUNCTION revoke_removed_org_member_credentials();

-- A mint/rotation that raced removal must also take the membership lock.
-- Personal credentials do not depend on membership in a company org.
CREATE FUNCTION check_org_credential_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_org uuid;
    is_personal boolean;
BEGIN
    IF NEW.status = 'revoked' THEN RETURN NEW; END IF;
    IF TG_TABLE_NAME = 'deploy_tokens' THEN
        SELECT a.org_id INTO target_org FROM apps a WHERE a.id = NEW.app_id;
    ELSE
        target_org := NEW.org_id;
    END IF;
    SELECT o.personal_org INTO is_personal FROM orgs o WHERE o.id = target_org;
    IF is_personal IS FALSE THEN
        PERFORM 1 FROM org_memberships
        WHERE org_id = target_org AND account_id = NEW.account_id AND removed_at IS NULL
        FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'organization access revoked' USING ERRCODE = '42501';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER org_api_key_membership
BEFORE INSERT OR UPDATE OF status, org_id, account_id ON api_keys
FOR EACH ROW EXECUTE FUNCTION check_org_credential_membership();
CREATE TRIGGER org_deploy_token_membership
BEFORE INSERT OR UPDATE OF status, app_id, account_id ON deploy_tokens
FOR EACH ROW EXECUTE FUNCTION check_org_credential_membership();
-- Revoke credentials of members who left before this migration as well.
UPDATE api_keys k SET status = 'revoked', revoked_at = coalesce(k.revoked_at, now())
FROM org_memberships m WHERE k.org_id = m.org_id AND k.account_id = m.account_id
  AND m.removed_at IS NOT NULL AND k.status <> 'revoked';
UPDATE deploy_tokens t SET status = 'revoked', revoked_at = coalesce(t.revoked_at, now())
FROM apps a, org_memberships m
WHERE t.app_id = a.id AND a.org_id = m.org_id AND t.account_id = m.account_id
  AND m.removed_at IS NOT NULL AND t.status <> 'revoked';
UPDATE org_invitations i SET revoked_at = coalesce(i.revoked_at, now())
FROM org_memberships m, accounts a
WHERE i.org_id = m.org_id AND a.id = m.account_id AND lower(i.email::text) = lower(a.email::text)
  AND m.removed_at IS NOT NULL AND i.consumed_at IS NULL AND i.revoked_at IS NULL;
END;
$org_ownership_replay$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS org_deploy_token_membership ON deploy_tokens;
DROP TRIGGER IF EXISTS org_api_key_membership ON api_keys;
DROP FUNCTION IF EXISTS check_org_credential_membership();
DROP TRIGGER IF EXISTS org_member_credentials_revoked ON org_memberships;
DROP FUNCTION IF EXISTS revoke_removed_org_member_credentials();
DROP TRIGGER IF EXISTS org_membership_access_revision ON org_memberships;
DROP FUNCTION IF EXISTS advance_org_access_revision();
ALTER TABLE org_memberships DROP CONSTRAINT IF EXISTS org_memberships_access_revision_chk;
ALTER TABLE org_memberships DROP COLUMN IF EXISTS access_revision;
