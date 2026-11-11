-- filename: 20261110163529554_deploy_token_minter.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- The member who minted (or last rotated) a deploy token. account_id stays
-- the app's creator, which scopes listing and revocation; a token with a
-- minter authenticates as that member, so a company app's token keeps only
-- the minter's current membership and role (account-flow audit 2026-11-10,
-- ADR 20261110190000). NULL is a token minted before this column existed and
-- still authenticates as account_id. Deleting the minter deletes the token
-- rather than handing it to the creator.
ALTER TABLE deploy_tokens
    ADD COLUMN IF NOT EXISTS created_by_account_id uuid REFERENCES accounts(id) ON DELETE CASCADE;

-- A live company-app token needs its minter, as well as the app's creator,
-- to be an active member.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.check_org_credential_membership() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
        -- Nested: plpgsql resolves NEW.created_by_account_id even when an
        -- AND's first operand is false, and api_keys has no such column.
        IF TG_TABLE_NAME = 'deploy_tokens' THEN
            IF NEW.created_by_account_id IS NOT NULL THEN
                PERFORM 1 FROM org_memberships
                WHERE org_id = target_org AND account_id = NEW.created_by_account_id AND removed_at IS NULL
                FOR SHARE;
                IF NOT FOUND THEN
                    RAISE EXCEPTION 'organization access revoked' USING ERRCODE = '42501';
                END IF;
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Removing a member also revokes the tokens they minted on other members'
-- apps.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.revoke_removed_org_member_credentials() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.removed_at IS NOT NULL AND OLD.removed_at IS NULL THEN
        UPDATE api_keys SET status = 'revoked', revoked_at = coalesce(revoked_at, now())
        WHERE org_id = NEW.org_id AND account_id = NEW.account_id AND status <> 'revoked';
        UPDATE deploy_tokens t SET status = 'revoked', revoked_at = coalesce(t.revoked_at, now())
        FROM apps a WHERE t.app_id = a.id AND a.org_id = NEW.org_id
          AND (t.account_id = NEW.account_id OR t.created_by_account_id = NEW.account_id)
          AND t.status <> 'revoked';
        UPDATE org_invitations SET revoked_at = coalesce(revoked_at, now())
        WHERE org_id = NEW.org_id AND consumed_at IS NULL AND revoked_at IS NULL
          AND lower(email::text) = (SELECT lower(email::text) FROM accounts WHERE id = NEW.account_id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS org_deploy_token_membership ON deploy_tokens;
CREATE TRIGGER org_deploy_token_membership
BEFORE INSERT OR UPDATE OF status, app_id, account_id, created_by_account_id ON deploy_tokens
FOR EACH ROW EXECUTE FUNCTION check_org_credential_membership();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS org_deploy_token_membership ON deploy_tokens;
CREATE TRIGGER org_deploy_token_membership
BEFORE INSERT OR UPDATE OF status, app_id, account_id ON deploy_tokens
FOR EACH ROW EXECUTE FUNCTION check_org_credential_membership();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.revoke_removed_org_member_credentials() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.check_org_credential_membership() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
-- +goose StatementEnd
ALTER TABLE deploy_tokens
    DROP COLUMN IF EXISTS created_by_account_id;
