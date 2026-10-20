-- Joining an organization by email domain (ADR-1190). An organization that
-- verified a domain with a DNS TXT record (sql/0007) may let people whose
-- proven address is at that domain join it: offered to them ('suggest') or
-- at once ('automatic'), with a role other than owner. A domain is verified
-- by at most one organization, so an address points to one organization.
--
-- An address counts as proven only while the front reports that the app
-- confirms addresses (domain_join_settings, written at startup): with
-- AUTH_AUTOCONFIRM anyone could claim an address at any domain. Domain join
-- adds a person once: whoever has left an organization or been removed
-- comes back only by invitation, so membership_history records every
-- membership ever made.
SET LOCAL lock_timeout = '5s';

ALTER TABLE gregale_auth.org_domains
  ADD COLUMN join_mode text NOT NULL DEFAULT 'off' CHECK (join_mode IN ('off', 'suggest', 'automatic')),
  ADD COLUMN join_role text NOT NULL DEFAULT 'member' REFERENCES gregale_auth.roles (name) CHECK (join_role <> 'owner'),
  ADD CONSTRAINT org_domains_join_needs_verification CHECK (join_mode = 'off' OR verified_at IS NOT NULL);

CREATE TABLE gregale_auth.domain_join_settings (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled boolean NOT NULL DEFAULT false
);
INSERT INTO gregale_auth.domain_join_settings DEFAULT VALUES;

CREATE TABLE gregale_auth.membership_history (
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES auth.users (id) ON DELETE CASCADE,
  first_joined_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);
CREATE INDEX membership_history_user_idx ON gregale_auth.membership_history (user_id);
INSERT INTO gregale_auth.membership_history (org_id, user_id, first_joined_at)
  SELECT org_id, user_id, created_at FROM gregale_auth.memberships ON CONFLICT DO NOTHING;

CREATE FUNCTION gregale_auth.record_membership() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
  INSERT INTO gregale_auth.membership_history (org_id, user_id) VALUES (NEW.org_id, NEW.user_id) ON CONFLICT DO NOTHING;
  RETURN NULL;
END $$;
CREATE TRIGGER gregale_auth_membership_history AFTER INSERT ON gregale_auth.memberships
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.record_membership();

-- proven_email_domain is the domain of the user's address when it is proven:
-- confirmed while the app confirms addresses, for a user who is neither
-- anonymous, deleted nor banned. An identity provider vouches only for its
-- own domains, as with invitations (sql/0005).
CREATE FUNCTION gregale_auth.proven_email_domain(subject uuid) RETURNS text
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT lower(substring(u.email FROM '@([^@]+)$')) FROM auth.users u
  WHERE u.id = subject AND u.email_confirmed_at IS NOT NULL AND u.deleted_at IS NULL AND NOT u.is_anonymous
    AND (u.banned_until IS NULL OR u.banned_until <= now())
    AND (SELECT s.enabled FROM gregale_auth.domain_join_settings s)
    AND (NOT u.is_sso_user OR EXISTS (
      SELECT 1 FROM auth.identities i JOIN auth.sso_domains d ON i.provider = 'sso:' || d.sso_provider_id::text
      WHERE i.user_id = u.id AND lower(d.domain) = lower(substring(u.email FROM '@([^@]+)$'))))
$$;

-- domain_join_offers is the organization (at most one) the user may join by
-- domain: one that verified the user's proven domain, with joining on, that
-- the user has never belonged to.
CREATE FUNCTION gregale_auth.domain_join_offers(subject uuid)
RETURNS TABLE (org_id uuid, domain text, join_mode text, join_role text)
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT d.org_id, d.domain, d.join_mode, d.join_role FROM gregale_auth.org_domains d
  WHERE d.domain = gregale_auth.proven_email_domain(subject) AND d.verified_at IS NOT NULL AND d.join_mode <> 'off'
    AND NOT EXISTS (SELECT 1 FROM gregale_auth.membership_history h WHERE h.org_id = d.org_id AND h.user_id = subject)
$$;

-- With 'automatic', people join when their address is confirmed or
-- changes, and identity-provider users when their SSO identity is made.
-- These run inside GoTrue's transactions and add nothing that can fail
-- there: the role is a foreign key and conflicts are ignored.
CREATE FUNCTION gregale_auth.join_by_email_domain() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
DECLARE
  subject uuid := (to_jsonb(NEW) ->> CASE TG_TABLE_NAME WHEN 'users' THEN 'id' ELSE 'user_id' END)::uuid;
BEGIN
  INSERT INTO gregale_auth.memberships (org_id, user_id, role)
    SELECT o.org_id, subject, o.join_role FROM gregale_auth.domain_join_offers(subject) o WHERE o.join_mode = 'automatic'
    ON CONFLICT (org_id, user_id) DO NOTHING;
  RETURN NULL;
END $$;
CREATE TRIGGER gregale_auth_domain_join_new AFTER INSERT ON auth.users
  FOR EACH ROW WHEN (NEW.email_confirmed_at IS NOT NULL) EXECUTE FUNCTION gregale_auth.join_by_email_domain();
CREATE TRIGGER gregale_auth_domain_join_confirmed AFTER UPDATE OF email_confirmed_at, email ON auth.users
  FOR EACH ROW WHEN (NEW.email_confirmed_at IS NOT NULL AND (OLD.email_confirmed_at IS NULL OR OLD.email IS DISTINCT FROM NEW.email))
  EXECUTE FUNCTION gregale_auth.join_by_email_domain();
CREATE TRIGGER gregale_auth_domain_join_sso AFTER INSERT ON auth.identities
  FOR EACH ROW WHEN (NEW.provider LIKE 'sso:%') EXECUTE FUNCTION gregale_auth.join_by_email_domain();

-- Domains are managed by holders of domains:manage, or of sso:manage as
-- before; owners hold both.
CREATE FUNCTION gregale_auth.require_domain_manager(org uuid) RETURNS uuid
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
BEGIN
  IF NOT (gregale_auth.has_permission(org, 'domains:manage') OR gregale_auth.has_permission(org, 'sso:manage')) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN caller;
END $$;

CREATE OR REPLACE FUNCTION gregale_auth.add_org_domain(org uuid, requested text, new_token text) RETURNS gregale_auth.org_domains
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  added gregale_auth.org_domains;
BEGIN
  PERFORM gregale_auth.require_domain_manager(org);
  IF EXISTS (SELECT 1 FROM gregale_auth.org_domains WHERE domain = lower(requested) AND verified_at IS NOT NULL AND org_id <> org) THEN
    RAISE EXCEPTION 'domain_taken' USING ERRCODE = '23505';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.org_domains WHERE org_id = org) >= 50 THEN
    RAISE EXCEPTION 'domain_limit' USING ERRCODE = '54000';
  END IF;
  INSERT INTO gregale_auth.org_domains (org_id, domain, token) VALUES (org, lower(requested), new_token)
    ON CONFLICT (org_id, domain) DO UPDATE SET token = gregale_auth.org_domains.token
    RETURNING * INTO added;
  RETURN added;
EXCEPTION
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_domain' USING ERRCODE = '22023';
END $$;

DROP FUNCTION gregale_auth.list_org_domains(uuid);
CREATE FUNCTION gregale_auth.list_org_domains(org uuid)
RETURNS TABLE (domain text, token text, verified_at timestamptz, created_at timestamptz, join_mode text, join_role text)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_domain_manager(org);
  RETURN QUERY SELECT d.domain, d.token, d.verified_at, d.created_at, d.join_mode, d.join_role
    FROM gregale_auth.org_domains d WHERE d.org_id = org ORDER BY d.domain;
END $$;

CREATE OR REPLACE FUNCTION gregale_auth.verify_org_domain(org uuid, requested text) RETURNS gregale_auth.org_domains
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  verified gregale_auth.org_domains;
BEGIN
  PERFORM gregale_auth.require_domain_manager(org);
  UPDATE gregale_auth.org_domains SET verified_at = coalesce(verified_at, now())
    WHERE org_id = org AND domain = lower(requested) RETURNING * INTO verified;
  IF verified.domain IS NULL THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  RETURN verified;
EXCEPTION
  WHEN unique_violation THEN RAISE EXCEPTION 'domain_taken' USING ERRCODE = '23505';
END $$;

CREATE OR REPLACE FUNCTION gregale_auth.remove_org_domain(org uuid, requested text) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_domain_manager(org);
  IF EXISTS (SELECT 1 FROM auth.sso_domains d JOIN gregale_auth.sso_organizations s ON s.provider_id = d.sso_provider_id
             WHERE s.org_id = org AND lower(d.domain) = lower(requested)) THEN
    RAISE EXCEPTION 'domain_in_use' USING ERRCODE = '23503';
  END IF;
  DELETE FROM gregale_auth.org_domains WHERE org_id = org AND domain = lower(requested);
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
END $$;

-- set_domain_join turns joining by a verified domain off, to 'suggest' or to
-- 'automatic' with a role other than owner. Turning on 'automatic' also
-- adds the people already proven at the domain who never belonged.
CREATE FUNCTION gregale_auth.set_domain_join(org uuid, requested text, mode text, member_role text)
RETURNS TABLE (domain text, join_mode text, join_role text, joined integer)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  existing gregale_auth.org_domains;
  added integer := 0;
BEGIN
  PERFORM gregale_auth.require_domain_manager(org);
  PERFORM 1 FROM gregale_auth.organizations o WHERE o.id = org FOR UPDATE;
  IF mode IS NULL OR mode NOT IN ('off', 'suggest', 'automatic') THEN
    RAISE EXCEPTION 'invalid_join_mode' USING ERRCODE = '22023';
  END IF;
  IF member_role IS NULL OR member_role = 'owner' OR NOT EXISTS (SELECT 1 FROM gregale_auth.roles r WHERE r.name = member_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  SELECT * INTO existing FROM gregale_auth.org_domains d WHERE d.org_id = org AND d.domain = lower(requested) FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF existing.verified_at IS NULL AND mode <> 'off' THEN
    RAISE EXCEPTION 'domain_not_verified' USING ERRCODE = '42501';
  END IF;
  UPDATE gregale_auth.org_domains d SET join_mode = mode, join_role = member_role WHERE d.org_id = org AND d.domain = existing.domain;
  IF mode = 'automatic' THEN
    INSERT INTO gregale_auth.memberships (org_id, user_id, role)
    SELECT org, u.id, member_role FROM auth.users u
    WHERE lower(u.email) LIKE '%@' || existing.domain AND gregale_auth.proven_email_domain(u.id) = existing.domain
      AND NOT EXISTS (SELECT 1 FROM gregale_auth.membership_history h WHERE h.org_id = org AND h.user_id = u.id)
    ON CONFLICT (org_id, user_id) DO NOTHING;
    GET DIAGNOSTICS added = ROW_COUNT;
  END IF;
  RETURN QUERY SELECT existing.domain, mode, member_role, added;
END $$;

-- suggested_organizations lists the organization the caller may join by
-- domain; join_organization joins it with the domain's role.
CREATE FUNCTION gregale_auth.suggested_organizations()
RETURNS TABLE (id uuid, slug text, name text, domain text, role text)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE caller uuid := gregale_auth.require_session();
BEGIN
  RETURN QUERY SELECT o.id, o.slug, o.name, x.domain, x.join_role
    FROM gregale_auth.domain_join_offers(caller) x JOIN gregale_auth.organizations o ON o.id = x.org_id;
END $$;

CREATE FUNCTION gregale_auth.join_organization(org uuid) RETURNS gregale_auth.memberships
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  offer record;
  joined gregale_auth.memberships;
BEGIN
  PERFORM 1 FROM gregale_auth.organizations o WHERE o.id = org FOR UPDATE;
  IF EXISTS (SELECT 1 FROM gregale_auth.memberships m WHERE m.org_id = org AND m.user_id = caller) THEN
    RAISE EXCEPTION 'already_member' USING ERRCODE = '23505';
  END IF;
  SELECT * INTO offer FROM gregale_auth.domain_join_offers(caller) x WHERE x.org_id = org;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  INSERT INTO gregale_auth.memberships (org_id, user_id, role) VALUES (org, caller, offer.join_role) RETURNING * INTO joined;
  RETURN joined;
END $$;

REVOKE ALL ON FUNCTION gregale_auth.record_membership(), gregale_auth.proven_email_domain(uuid), gregale_auth.domain_join_offers(uuid),
  gregale_auth.join_by_email_domain(), gregale_auth.require_domain_manager(uuid), gregale_auth.list_org_domains(uuid),
  gregale_auth.set_domain_join(uuid, text, text, text), gregale_auth.suggested_organizations(), gregale_auth.join_organization(uuid) FROM PUBLIC;
