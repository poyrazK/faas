-- Self-service identity providers (ADR-1190). GoTrue takes the address a
-- SAML identity provider asserts and marks it confirmed, whatever its
-- domain. A provider the app's operator registered is trusted for that; one
-- an organization connected itself (sql/0007) is not: its people must have
-- addresses at the provider's domains, which the organization verified, and
-- the app's invitation or waitlist rules apply to them. self_service marks
-- the providers an organization created or changed through the organization
-- API. Providers linked before this migration count as the operator's.
SET LOCAL lock_timeout = '5s';

ALTER TABLE gregale_auth.sso_organizations ADD COLUMN self_service boolean NOT NULL DEFAULT false;

-- link_org_sso_provider links a provider the organization just created and
-- marks it self-service in one step, so it is never trusted as the
-- operator's in between.
CREATE FUNCTION gregale_auth.link_org_sso_provider(org uuid, idp uuid, member_role text) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.check_org_sso(org, NULL, NULL, member_role);
  PERFORM gregale_auth.link_sso_organization(idp, org, member_role);
  UPDATE gregale_auth.sso_organizations SET self_service = true WHERE provider_id = idp;
END $$;

-- claim_org_sso_provider marks a provider linked to the organization as
-- self-service before the organization changes its metadata or domains.
CREATE FUNCTION gregale_auth.claim_org_sso_provider(org uuid, idp uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.check_org_sso(org, idp, NULL, NULL);
  UPDATE gregale_auth.sso_organizations SET self_service = true WHERE provider_id = idp AND org_id = org;
END $$;

-- sso_signup_rules answers the before-user-created hook for a user an
-- identity provider is about to create: whether the provider is
-- self-service, and whether the address is at one of its domains.
CREATE FUNCTION gregale_auth.sso_signup_rules(idp uuid, address_domain text)
RETURNS TABLE (self_service boolean, in_domain boolean)
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT coalesce((SELECT s.self_service FROM gregale_auth.sso_organizations s WHERE s.provider_id = idp), false),
    EXISTS (SELECT 1 FROM auth.sso_domains d WHERE d.sso_provider_id = idp AND lower(d.domain) = lower(address_domain))
$$;

REVOKE ALL ON FUNCTION gregale_auth.link_org_sso_provider(uuid, uuid, text), gregale_auth.claim_org_sso_provider(uuid, uuid),
  gregale_auth.sso_signup_rules(uuid, text) FROM PUBLIC;
