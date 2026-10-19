-- Self-service organization SSO (ADR-1190). When the app owner turns it on,
-- an organization's owners (and members whose role grants sso:manage)
-- prove they control an email domain with a DNS TXT record and then connect
-- their own SAML identity provider for verified domains only. A domain is
-- verified by at most one organization, so no organization can route
-- another's people to its identity provider.

CREATE TABLE gregale_auth.org_domains (
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
  domain text NOT NULL CHECK (char_length(domain) <= 253 AND domain ~ '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$'),
  token text NOT NULL CHECK (token ~ '^[A-Za-z0-9_-]{32}$'),
  verified_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, domain)
);
CREATE UNIQUE INDEX org_domains_verified_idx ON gregale_auth.org_domains (domain) WHERE verified_at IS NOT NULL;

CREATE FUNCTION gregale_auth.require_sso_manager(org uuid) RETURNS uuid
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
BEGIN
  IF NOT gregale_auth.has_permission(org, 'sso:manage') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN caller;
END $$;

CREATE FUNCTION gregale_auth.add_org_domain(org uuid, requested text, new_token text) RETURNS gregale_auth.org_domains
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  added gregale_auth.org_domains;
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
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

CREATE FUNCTION gregale_auth.list_org_domains(org uuid)
RETURNS TABLE (domain text, token text, verified_at timestamptz, created_at timestamptz)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
  RETURN QUERY SELECT d.domain, d.token, d.verified_at, d.created_at FROM gregale_auth.org_domains d WHERE d.org_id = org ORDER BY d.domain;
END $$;

-- verify_org_domain records a verification the front has checked in DNS.
CREATE FUNCTION gregale_auth.verify_org_domain(org uuid, requested text) RETURNS gregale_auth.org_domains
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  verified gregale_auth.org_domains;
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
  UPDATE gregale_auth.org_domains SET verified_at = coalesce(verified_at, now())
    WHERE org_id = org AND domain = lower(requested) RETURNING * INTO verified;
  IF verified.domain IS NULL THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  RETURN verified;
EXCEPTION
  WHEN unique_violation THEN RAISE EXCEPTION 'domain_taken' USING ERRCODE = '23505';
END $$;

-- remove_org_domain refuses a domain that one of the organization's identity
-- providers still serves.
CREATE FUNCTION gregale_auth.remove_org_domain(org uuid, requested text) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
  IF EXISTS (SELECT 1 FROM auth.sso_domains d JOIN gregale_auth.sso_organizations s ON s.provider_id = d.sso_provider_id
             WHERE s.org_id = org AND lower(d.domain) = lower(requested)) THEN
    RAISE EXCEPTION 'domain_in_use' USING ERRCODE = '23503';
  END IF;
  DELETE FROM gregale_auth.org_domains WHERE org_id = org AND domain = lower(requested);
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
END $$;

-- check_org_sso allows an identity provider for the organization only for
-- domains it verified and with a role other than owner, and at most three
-- providers per organization. provider is NULL for a new one.
CREATE FUNCTION gregale_auth.check_org_sso(org uuid, provider uuid, domains text[], member_role text) RETURNS void
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
  IF provider IS NOT NULL AND NOT EXISTS (SELECT 1 FROM gregale_auth.sso_organizations WHERE provider_id = provider AND org_id = org) THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF provider IS NULL AND (SELECT count(*) FROM gregale_auth.sso_organizations WHERE org_id = org) >= 3 THEN
    RAISE EXCEPTION 'sso_provider_limit' USING ERRCODE = '54000';
  END IF;
  IF domains IS NOT NULL AND EXISTS (SELECT 1 FROM unnest(domains) AS requested(domain)
      WHERE NOT EXISTS (SELECT 1 FROM gregale_auth.org_domains d WHERE d.org_id = org AND d.domain = lower(requested.domain) AND d.verified_at IS NOT NULL)) THEN
    RAISE EXCEPTION 'domain_not_verified' USING ERRCODE = '42501';
  END IF;
  IF member_role IS NOT NULL AND (member_role = 'owner' OR NOT EXISTS (SELECT 1 FROM gregale_auth.roles WHERE name = member_role)) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
END $$;

CREATE FUNCTION gregale_auth.org_sso_providers(org uuid) RETURNS TABLE (provider_id uuid, role text)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
  RETURN QUERY SELECT s.provider_id, s.role FROM gregale_auth.sso_organizations s WHERE s.org_id = org ORDER BY s.created_at;
END $$;

REVOKE ALL ON FUNCTION gregale_auth.require_sso_manager(uuid), gregale_auth.add_org_domain(uuid, text, text), gregale_auth.list_org_domains(uuid),
  gregale_auth.verify_org_domain(uuid, text), gregale_auth.remove_org_domain(uuid, text), gregale_auth.check_org_sso(uuid, uuid, text[], text),
  gregale_auth.org_sso_providers(uuid) FROM PUBLIC;
