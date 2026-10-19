-- Anonymous users (ADR-1190). Anyone can become one without an address, so
-- they may not create organizations: an organization's owner must be a
-- person the application can reach. Linking an email address, phone or
-- provider makes the user permanent (auth.users.is_anonymous turns false).
CREATE OR REPLACE FUNCTION gregale_auth.create_organization(org_slug text, org_name text) RETURNS gregale_auth.organizations
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  created gregale_auth.organizations;
BEGIN
  IF (SELECT is_anonymous FROM auth.users WHERE id = caller) THEN
    RAISE EXCEPTION 'anonymous_user' USING ERRCODE = '42501';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.organizations WHERE created_by = caller) >= 100 THEN
    RAISE EXCEPTION 'organization_limit' USING ERRCODE = '54000';
  END IF;
  INSERT INTO gregale_auth.organizations (slug, name, created_by) VALUES (org_slug, org_name, caller) RETURNING * INTO created;
  INSERT INTO gregale_auth.memberships (org_id, user_id, role) VALUES (created.id, caller, 'owner');
  RETURN created;
EXCEPTION
  WHEN unique_violation THEN RAISE EXCEPTION 'slug_taken' USING ERRCODE = '23505';
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_organization' USING ERRCODE = '22023';
END $$;
