-- SCIM provisioning for organization SSO (ADR-1190). A customer's identity
-- provider provisions and deprovisions its people through /scim/v2/Users
-- with a bearer token scoped to one SSO provider. A provisioned person is a
-- SCIM record until their first SAML sign-in, which links the record to the
-- GoTrue user it creates; deprovisioning a linked person bans them, ends
-- their sessions and removes them from the provider's organization.

CREATE TABLE gregale_auth.scim_tokens (
  provider_id uuid PRIMARY KEY REFERENCES auth.sso_providers (id) ON DELETE CASCADE,
  -- Only a SHA-256 of the token is stored; the token is shown once.
  token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gregale_auth.scim_users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES auth.sso_providers (id) ON DELETE CASCADE,
  external_id text CHECK (char_length(external_id) BETWEEN 1 AND 256),
  -- user_name is the person's sign-in name at the identity provider, in
  -- practice their email address; email is the primary address if given.
  user_name text NOT NULL CHECK (char_length(user_name) BETWEEN 1 AND 320),
  email text CHECK (char_length(email) BETWEEN 3 AND 320),
  display_name text CHECK (char_length(display_name) <= 256),
  given_name text CHECK (char_length(given_name) <= 128),
  family_name text CHECK (char_length(family_name) <= 128),
  active boolean NOT NULL DEFAULT true,
  user_id uuid UNIQUE REFERENCES auth.users (id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX scim_users_user_name_idx ON gregale_auth.scim_users (provider_id, lower(user_name));
CREATE UNIQUE INDEX scim_users_external_id_idx ON gregale_auth.scim_users (provider_id, external_id) WHERE external_id IS NOT NULL;

-- link_scim_user runs before the organization join (trigger names sort
-- 'scim' before 'sso'): an SSO user's first sign-in links the SCIM record
-- with their user name or email, and a person the identity provider has
-- deprovisioned is banned at once, so session_active() refuses them and
-- they join no organization.
CREATE FUNCTION gregale_auth.link_scim_user() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
DECLARE
  signed_in text;
  linked gregale_auth.scim_users;
BEGIN
  IF NEW.provider NOT LIKE 'sso:%' THEN
    RETURN NULL;
  END IF;
  SELECT lower(u.email) INTO signed_in FROM auth.users u WHERE u.id = NEW.user_id;
  UPDATE gregale_auth.scim_users s SET user_id = NEW.user_id, updated_at = now()
  WHERE s.id = (SELECT c.id FROM gregale_auth.scim_users c
                WHERE c.provider_id::text = substr(NEW.provider, 5) AND c.user_id IS NULL
                  AND (lower(c.user_name) = signed_in OR lower(c.email) = signed_in)
                ORDER BY c.created_at LIMIT 1)
  RETURNING * INTO linked;
  IF linked.id IS NOT NULL AND NOT linked.active THEN
    UPDATE auth.users SET banned_until = now() + interval '100 years' WHERE id = NEW.user_id;
  END IF;
  RETURN NULL;
END
$$;

CREATE TRIGGER gregale_auth_scim_link AFTER INSERT ON auth.identities
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.link_scim_user();

-- A banned person joins no organization through their identity provider.
CREATE OR REPLACE FUNCTION gregale_auth.join_sso_organization() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
  IF NEW.provider LIKE 'sso:%' AND NOT EXISTS (SELECT 1 FROM auth.users u WHERE u.id = NEW.user_id AND u.banned_until > now()) THEN
    INSERT INTO gregale_auth.memberships (org_id, user_id, role)
    SELECT s.org_id, NEW.user_id, s.role FROM gregale_auth.sso_organizations s
    WHERE s.provider_id::text = substr(NEW.provider, 5)
    ON CONFLICT (org_id, user_id) DO NOTHING;
  END IF;
  RETURN NULL;
END
$$;
