-- Organization single sign-on (ADR-1190). The app owner registers a SAML
-- identity provider in GoTrue (auth.sso_providers) and may link it to one
-- organization: users who sign in through it join that organization with
-- the linked role the first time, and existing users of the provider join
-- when the link is made.

CREATE TABLE gregale_auth.sso_organizations (
  provider_id uuid PRIMARY KEY REFERENCES auth.sso_providers (id) ON DELETE CASCADE,
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
  -- An identity provider never makes anyone an owner.
  role text NOT NULL REFERENCES gregale_auth.roles (name) CHECK (role <> 'owner'),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sso_organizations_org_idx ON gregale_auth.sso_organizations (org_id);

-- GoTrue names an SSO identity's provider 'sso:<provider id>'.
CREATE FUNCTION gregale_auth.join_sso_organization() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
  IF NEW.provider LIKE 'sso:%' THEN
    INSERT INTO gregale_auth.memberships (org_id, user_id, role)
    SELECT s.org_id, NEW.user_id, s.role FROM gregale_auth.sso_organizations s
    WHERE s.provider_id::text = substr(NEW.provider, 5)
    ON CONFLICT (org_id, user_id) DO NOTHING;
  END IF;
  RETURN NULL;
END
$$;

CREATE TRIGGER gregale_auth_sso_join AFTER INSERT ON auth.identities
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.join_sso_organization();

-- link_sso_organization links a provider to an organization, or unlinks it
-- when org is NULL, and adds the provider's existing users as members.
-- Members keep their memberships when a link changes or ends.
CREATE FUNCTION gregale_auth.link_sso_organization(idp uuid, org uuid, member_role text) RETURNS integer
LANGUAGE plpgsql SET search_path = '' AS $$
DECLARE
  joined integer;
BEGIN
  IF org IS NULL THEN
    DELETE FROM gregale_auth.sso_organizations WHERE provider_id = idp;
    RETURN 0;
  END IF;
  INSERT INTO gregale_auth.sso_organizations (provider_id, org_id, role) VALUES (idp, org, member_role)
    ON CONFLICT (provider_id) DO UPDATE SET org_id = EXCLUDED.org_id, role = EXCLUDED.role;
  INSERT INTO gregale_auth.memberships (org_id, user_id, role)
  SELECT DISTINCT org, i.user_id, member_role FROM auth.identities i WHERE i.provider = 'sso:' || idp::text
  ON CONFLICT (org_id, user_id) DO NOTHING;
  GET DIAGNOSTICS joined = ROW_COUNT;
  RETURN joined;
END
$$;
REVOKE ALL ON FUNCTION gregale_auth.link_sso_organization(uuid, uuid, text) FROM PUBLIC;

-- An identity provider vouches only for its own email domains. A user who
-- signed in through one may accept an invitation only for an address in one
-- of that provider's domains, so a provider cannot claim invitations sent to
-- another company's addresses.
CREATE OR REPLACE FUNCTION gregale_auth.accept_invitation(hashed_token bytea) RETURNS gregale_auth.memberships
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  pending gregale_auth.invitations;
  joined gregale_auth.memberships;
BEGIN
  SELECT * INTO pending FROM gregale_auth.invitations WHERE token_hash = hashed_token FOR UPDATE;
  IF NOT FOUND OR pending.accepted_at IS NOT NULL OR pending.revoked_at IS NOT NULL OR pending.expires_at <= now() THEN
    RAISE EXCEPTION 'invitation_invalid' USING ERRCODE = 'P0002';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM auth.users u WHERE u.id = caller AND lower(u.email) = pending.email AND u.email_confirmed_at IS NOT NULL
      AND (NOT u.is_sso_user OR EXISTS (
        SELECT 1 FROM auth.identities i JOIN auth.sso_domains d ON i.provider = 'sso:' || d.sso_provider_id::text
        WHERE i.user_id = u.id AND lower(d.domain) = split_part(pending.email, '@', 2)))) THEN
    RAISE EXCEPTION 'invitation_email_mismatch' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM gregale_auth.organizations WHERE id = pending.org_id FOR UPDATE;
  INSERT INTO gregale_auth.memberships (org_id, user_id, role) VALUES (pending.org_id, caller, pending.role)
    ON CONFLICT (org_id, user_id) DO NOTHING RETURNING * INTO joined;
  IF joined IS NULL THEN
    RAISE EXCEPTION 'already_member' USING ERRCODE = '23505';
  END IF;
  UPDATE gregale_auth.invitations SET accepted_at = now(), accepted_by = caller WHERE id = pending.id;
  RETURN joined;
END $$;
